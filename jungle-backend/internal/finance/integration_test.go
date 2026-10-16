package finance

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/database"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/money"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/wallet"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Set RUN_DB_INTEGRATION=1 to run against an actual PostgreSQL with Stage 4
// migrations applied (start the API once first). These tests create audit data.
func integrationService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	if os.Getenv("RUN_DB_INTEGRATION") != "1" {
		t.Skip("set RUN_DB_INTEGRATION=1 for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.NewPool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = pool.Ping(ctx); err != nil {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	var exists bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='wager_transactions')`).Scan(&exists); err != nil || !exists {
		t.Fatalf("run Stage 4 application to apply migrations first; query error: %v", err)
	}
	return NewService(pool), ctx
}
func uniqueID(t *testing.T) string {
	t.Helper()
	id, err := wallet.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func createFunded(t *testing.T, s *Service, ctx context.Context, balance string) wallet.Wallet {
	t.Helper()
	w, err := s.OpenWallet(ctx, OpeningCommand{PlayerID: "test-" + uniqueID(t), InitialBalance: money.JSONMoney{Amount: balance, Currency: "BRL"}})
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func bet(w wallet.Wallet, external string, amount string) Command {
	return Command{ProviderID: "integration-provider", ExternalTransactionID: external, PlayerID: w.PlayerID, WalletID: w.ID,
		RoundID: "round-1", GameID: "game-1", Kind: "BET", Money: money.JSONMoney{Amount: amount, Currency: "BRL"}}
}
func loadWallet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, w wallet.Wallet) (int64, int64) {
	t.Helper()
	var balance, version int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor_units,version FROM wallets WHERE id=$1`, w.ID).Scan(&balance, &version); err != nil {
		t.Fatal(err)
	}
	return balance, version
}
func ledgerCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, w wallet.Wallet, direction string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_id=$1 AND direction=$2`, w.ID, direction).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestConcurrentBetsIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "100.00")
	inputs := []Command{bet(w, "bet-"+uniqueID(t), "80.00"), bet(w, "bet-"+uniqueID(t), "80.00")}
	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for i := range inputs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.Process(ctx, inputs[i], "key-"+inputs[i].ExternalTransactionID)
		}(i)
	}
	wg.Wait()
	success, rejected := 0, 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		switch r.Status {
		case "PROCESSED":
			success++
		case "REJECTED":
			if r.FailureCode != "INSUFFICIENT_FUNDS" {
				t.Fatalf("unexpected failure: %+v", r)
			}
			rejected++
		default:
			t.Fatalf("unexpected result: %+v", r)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("expected 1 processed and 1 rejected; got %+v", results)
	}
	balance, version := loadWallet(t, ctx, s.DB, w)
	if balance != 2000 || version != 2 || ledgerCount(t, ctx, s.DB, w, "DEBIT") != 1 {
		t.Fatalf("inconsistent result balance=%d version=%d", balance, version)
	}
}
func TestRepeatedSameBetIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "100.00")
	cmd := bet(w, "bet-"+uniqueID(t), "25.00")
	// A new key per test run avoids collisions with persisted data in PostgreSQL.
	// All concurrent requests within this test MUST share this one key.
	stableKey := "stable-idempotency-key-" + uniqueID(t)
	otherKey := "another-idempotency-key-" + uniqueID(t)
	const n = 30
	results := make([]Result, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = s.Process(ctx, cmd, stableKey) }(i)
	}
	wg.Wait()
	var firstID string
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("request %d failed: %v", i, errs[i])
		}
		if r.Status != "PROCESSED" || r.Balance.Amount != "75.00" {
			t.Fatalf("wrong result %d: %+v", i, r)
		}
		if firstID == "" {
			firstID = r.TransactionID
		}
		if firstID != r.TransactionID {
			t.Fatalf("new transaction on replay: %s != %s", r.TransactionID, firstID)
		}
	}
	balance, version := loadWallet(t, ctx, s.DB, w)
	if balance != 7500 || version != 2 || ledgerCount(t, ctx, s.DB, w, "DEBIT") != 1 {
		t.Fatalf("duplicate debit: balance=%d,version=%d", balance, version)
	}
	// An attempt to reuse a key for different business data must conflict.
	changed := cmd
	changed.Money.Amount = "24.00"
	_, err := s.Process(ctx, changed, stableKey)
	if err != ErrConflict {
		t.Fatalf("expected key conflict; got %v", err)
	}
	// A new key with same external ID must also conflict.
	_, err = s.Process(ctx, cmd, otherKey)
	if err != ErrConflict {
		t.Fatalf("expected external ID conflict; got %v", err)
	}
}
func TestWinAndImmutableLedgerIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "10.00")
	cmd := bet(w, "win-"+uniqueID(t), "7.50")
	cmd.Kind = "WIN"
	result, err := s.Process(ctx, cmd, "win-key-"+cmd.ExternalTransactionID)
	if err != nil || result.Balance.Amount != "17.50" || result.Status != "PROCESSED" {
		t.Fatalf("credit: %+v, %v", result, err)
	}
	balance, version := loadWallet(t, ctx, s.DB, w)
	if balance != 1750 || version != 2 {
		t.Fatalf("incorrect credit: %d version=%d", balance, version)
	}
	// This schema must independently reject attempts to mutate the ledger.
	var id string
	if err := s.DB.QueryRow(ctx, "SELECT id FROM ledger_entries WHERE wallet_id=$1 AND entry_type='WIN'", w.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(ctx, `UPDATE ledger_entries SET amount_minor_units=1 WHERE id=$1`, id)
	if err == nil {
		t.Fatal("ledger UPDATE succeeded, should be append-only")
	}
	_, err = s.DB.Exec(ctx, `DELETE FROM ledger_entries WHERE id=$1`, id)
	if err == nil {
		t.Fatal("ledger DELETE succeeded, should be append-only")
	}
	var old int64
	if err := s.DB.QueryRow(ctx, "SELECT amount_minor_units FROM ledger_entries WHERE id=$1", id).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if old != 750 {
		t.Fatal(fmt.Sprintf("ledger mutated: %d", old))
	}
}

// Reconciles the source-of-truth wallet with the append-only ledger and
// proves rejected bets do not create a movement or increment version.
func TestReconciliationAndInsufficientFundsIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "100.00")
	betCmd := bet(w, "bet-"+uniqueID(t), "80.00")
	result, err := s.Process(ctx, betCmd, "key-"+betCmd.ExternalTransactionID)
	if err != nil || result.Status != "PROCESSED" {
		t.Fatalf("BET: result=%+v err=%v", result, err)
	}
	winCmd := bet(w, "win-"+uniqueID(t), "30.00")
	winCmd.Kind = "WIN"
	result, err = s.Process(ctx, winCmd, "key-"+winCmd.ExternalTransactionID)
	if err != nil || result.Status != "PROCESSED" {
		t.Fatalf("WIN: result=%+v err=%v", result, err)
	}
	rejectedCmd := bet(w, "rejected-"+uniqueID(t), "80.00")
	rejectedKey := "key-" + rejectedCmd.ExternalTransactionID
	rejected, err := s.Process(ctx, rejectedCmd, rejectedKey)
	if err != nil || rejected.Status != "REJECTED" || rejected.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Fatalf("reject: result=%+v err=%v", rejected, err)
	}
	replay, err := s.Process(ctx, rejectedCmd, rejectedKey)
	if err != nil || replay != rejected {
		t.Fatalf("rejected transaction replay differs: original=%+v replay=%+v error=%v", rejected, replay, err)
	}
	balance, version := loadWallet(t, ctx, s.DB, w)
	if balance != 5000 || version != 3 {
		t.Fatalf("wallet incorrectly moved: balance=%d version=%d", balance, version)
	}
	var ledgerCount int
	var matches bool
	if err := s.DB.QueryRow(ctx, `SELECT ledger_entry_count, balance_minor_units = ledger_net_minor_units
		FROM stage4_wallet_audit WHERE wallet_id=$1`, w.ID).Scan(&ledgerCount, &matches); err != nil {
		t.Fatal(err)
	}
	if !matches || ledgerCount != 3 {
		t.Fatalf("ledger reconciliation failed: matches=%v ledgerCount=%d", matches, ledgerCount)
	}
}
