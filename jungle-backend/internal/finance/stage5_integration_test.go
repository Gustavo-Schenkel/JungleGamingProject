package finance

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/money"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/wallet"
)

func stage5Command(w wallet.Wallet, kind, external, amount, reference string) Command {
	return Command{ProviderID: "integration-provider", ExternalTransactionID: external,
		PlayerID: w.PlayerID, WalletID: w.ID, RoundID: "stage5-round", GameID: "stage5-game",
		Kind: kind, Money: money.JSONMoney{Amount: amount, Currency: "BRL"},
		ReferenceExternalTransactionID: reference}
}
func stage5Run(t *testing.T, s *Service, ctx context.Context, c Command) Result {
	t.Helper()
	result, err := s.Process(ctx, c, "key-"+c.ExternalTransactionID)
	if err != nil {
		t.Fatalf("%s %s: %v", c.Kind, c.ExternalTransactionID, err)
	}
	return result
}
func TestStage5RefundRollbackLossIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "100.00")
	betID := "bet-" + uniqueID(t)
	if got := stage5Run(t, s, ctx, stage5Command(w, "BET", betID, "40.00", "")); got.Status != "PROCESSED" || got.Balance.Amount != "60.00" {
		t.Fatal(got)
	}
	refundID := "refund-" + uniqueID(t)
	refund := stage5Run(t, s, ctx, stage5Command(w, "REFUND", refundID, "40.00", betID))
	if refund.Status != "PROCESSED" || refund.Balance.Amount != "100.00" || refund.WalletVersion != 3 {
		t.Fatal(refund)
	}
	repeated := stage5Run(t, s, ctx, stage5Command(w, "REFUND", "refund-"+uniqueID(t), "40.00", betID))
	if repeated.Status != "REJECTED" || repeated.FailureCode != "ALREADY_REVERSED" {
		t.Fatal(repeated)
	}
	loss := stage5Run(t, s, ctx, stage5Command(w, "LOSS", "loss-"+uniqueID(t), "0.00", ""))
	if loss.Status != "PROCESSED" || loss.WalletVersion != 3 || loss.Balance.Amount != "100.00" {
		t.Fatal(loss)
	}
	reverseRefund := stage5Run(t, s, ctx, stage5Command(w, "ROLLBACK", "rollback-"+uniqueID(t), "40.00", refundID))
	if reverseRefund.Status != "PROCESSED" || reverseRefund.WalletVersion != 4 || reverseRefund.Balance.Amount != "60.00" {
		t.Fatal(reverseRefund)
	}
	balance, version := loadWallet(t, ctx, s.DB, w)
	if balance != 6000 || version != 4 {
		t.Fatalf("unexpected wallet balance=%d version=%d", balance, version)
	}
	var count int
	var consistent bool
	if err := s.DB.QueryRow(ctx, `SELECT ledger_entry_count,balance_minor_units=ledger_net_minor_units FROM stage4_wallet_audit WHERE wallet_id=$1`, w.ID).Scan(&count, &consistent); err != nil {
		t.Fatal(err)
	}
	if count != 4 || !consistent {
		t.Fatalf("ledger entries=%d consistent=%v", count, consistent)
	}
}
func TestStage5RollbackWinNoFundsIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "0.00")
	winID := "win-" + uniqueID(t)
	if got := stage5Run(t, s, ctx, stage5Command(w, "WIN", winID, "20.00", "")); got.Status != "PROCESSED" {
		t.Fatal(got)
	}
	if got := stage5Run(t, s, ctx, stage5Command(w, "BET", "bet-"+uniqueID(t), "20.00", "")); got.Status != "PROCESSED" {
		t.Fatal(got)
	}
	rejected := stage5Run(t, s, ctx, stage5Command(w, "ROLLBACK", "rollback-"+uniqueID(t), "20.00", winID))
	if rejected.Status != "REJECTED" || rejected.FailureCode != "REVERSAL_INSUFFICIENT_FUNDS" || rejected.WalletVersion != 3 {
		t.Fatal(rejected)
	}
	if balance, _ := loadWallet(t, ctx, s.DB, w); balance != 0 {
		t.Fatalf("bad balance=%d", balance)
	}
}
func TestStage5PendingReferenceRecoveryIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "100.00")
	futureBet := "bet-" + uniqueID(t)
	pendingCmd := stage5Command(w, "REFUND", "refund-"+uniqueID(t), "10.00", futureBet)
	pending := stage5Run(t, s, ctx, pendingCmd)
	if pending.Status != "PENDING_REFERENCE" || pending.WalletVersion != 1 || pending.Balance.Amount != "100.00" {
		t.Fatal(pending)
	}
	if got := stage5Run(t, s, ctx, stage5Command(w, "BET", futureBet, "10.00", "")); got.Balance.Amount != "90.00" {
		t.Fatal(got)
	}
	// Either this service or the running application's worker may claim the retry.
	// Wait until the persisted retry interval is due; do not edit terminal records.
	time.Sleep(2300 * time.Millisecond)
	if err := s.RetryReference(ctx, pending.TransactionID); err != nil {
		t.Fatal(err)
	}
	replay := stage5Run(t, s, ctx, pendingCmd)
	if replay.Status != "PROCESSED" || replay.Balance.Amount != "100.00" || replay.WalletVersion != 3 || replay.TransactionID != pending.TransactionID {
		t.Fatal(replay)
	}
	if ledgerCount(t, ctx, s.DB, w, "CREDIT") != 2 {
		t.Fatal("expected OPENING and REFUND credits exactly once")
	}
}
func TestStage5CompetingReversalsIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "100.00")
	baseID := "bet-" + uniqueID(t)
	stage5Run(t, s, ctx, stage5Command(w, "BET", baseID, "80.00", ""))
	first := stage5Command(w, "REFUND", "refund-"+uniqueID(t), "80.00", baseID)
	second := stage5Command(w, "ROLLBACK", "rollback-"+uniqueID(t), "80.00", baseID)
	cmds := []Command{first, second}
	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for i := range cmds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.Process(ctx, cmds[i], "key-"+cmds[i].ExternalTransactionID)
		}(i)
	}
	wg.Wait()
	success, failed := 0, 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("reversal %d: %v", i, errs[i])
		}
		if r.Status == "PROCESSED" {
			success++
		} else if r.Status == "REJECTED" && r.FailureCode == "ALREADY_REVERSED" {
			failed++
		} else {
			t.Fatal(r)
		}
	}
	if success != 1 || failed != 1 {
		t.Fatalf("unexpected reversals: %+v", results)
	}
	if balance, _ := loadWallet(t, ctx, s.DB, w); balance != 10000 {
		t.Fatalf("double refund: %d", balance)
	}
}
