package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/database"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/money"
)

// RUN_DB_INTEGRATION=1 requires an already migrated PostgreSQL Docker database.
func TestInboxAtomicityAndExactReplayIntegration(t *testing.T) {
	if os.Getenv("RUN_DB_INTEGRATION") != "1" {
		t.Skip("set RUN_DB_INTEGRATION=1")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.NewPool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	providerClient := "test-inbox-client"
	cfg.OIDCProviderClients[providerClient] = "test-inbox-provider"
	cfg.SQSCommandSecrets[providerClient] = "integration-secret-key-with-over-thirty-two-characters"
	service := finance.NewService(pool)
	uniq := freshDedupID()
	opening, err := service.OpenWallet(ctx, finance.OpeningCommand{PlayerID: "inbox-test-" + uniq, InitialBalance: money.JSONMoney{Amount: "100.00", Currency: "BRL"}})
	if err != nil {
		t.Fatal(err)
	}
	command := finance.Command{ExternalTransactionID: "msg-" + uniq, WalletID: opening.ID, PlayerID: opening.PlayerID, Kind: "BET", RoundID: "round-1", GameID: "game-1", Money: money.JSONMoney{Amount: "80.00", Currency: "BRL"}}
	e, err := SignCommand(cfg, SignedCommand{SchemaVersion: 1, ClientID: providerClient, IdempotencyKey: "inbox-key-" + uniq, Command: command})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	q := &Queue{cfg: cfg}
	firstID := "sqs-message-" + uniq
	// Crash simulation: process wallet+ledger+outbox inside a transaction, then
	// ROLLBACK. Nothing can become visible, including a tentative inbox record.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO sqs_inbox(consumer_name,message_id,provider_id,idempotency_key,transaction_id,result_status)
 VALUES('jungle-financial-v1',$1,'test-inbox-provider',$2,NULL,'PROCESSING')`, firstID, e.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ProcessInTx(ctx, tx, finance.Command{ProviderID: "test-inbox-provider", ExternalTransactionID: command.ExternalTransactionID,
		PlayerID: command.PlayerID, WalletID: command.WalletID, Kind: command.Kind, RoundID: command.RoundID, GameID: command.GameID, Money: command.Money}, e.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(balance int64, ledger int, outbox int, inbox int) {
		t.Helper()
		var b int64
		var l, o, i int
		if err = pool.QueryRow(ctx, `SELECT balance_minor_units FROM wallets WHERE id=$1`, opening.ID).Scan(&b); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_id=$1`, opening.ID).Scan(&l); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM sqs_outbox WHERE aggregate_id=$1`, opening.ID).Scan(&o); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM sqs_inbox WHERE message_id IN ($1,$2)`, firstID, "second-"+uniq).Scan(&i); err != nil {
			t.Fatal(err)
		}
		if b != balance || l != ledger || o != outbox || i != inbox {
			t.Fatalf("wallet=%d ledger=%d outbox=%d inbox=%d expected %d/%d/%d/%d", b, l, o, i, balance, ledger, outbox, inbox)
		}
	}
	check(10000, 1, 1, 0)
	if err = q.consumeOne(ctx, pool, service, firstID, string(data)); err != nil {
		t.Fatal(err)
	}
	check(2000, 2, 2, 1)
	if err = q.consumeOne(ctx, pool, service, firstID, string(data)); err != nil {
		t.Fatal(err)
	}
	check(2000, 2, 2, 1)
	if err = q.consumeOne(ctx, pool, service, "second-"+uniq, string(data)); err != nil {
		t.Fatal(err)
	}
	check(2000, 2, 2, 2)
	var status string
	err = pool.QueryRow(ctx, `SELECT status FROM wager_transactions WHERE provider_id='test-inbox-provider' AND external_transaction_id=$1`, command.ExternalTransactionID).Scan(&status)
	if err != nil || status != "PROCESSED" {
		t.Fatal(fmt.Errorf("unexpected transaction status %q (%v)", status, err))
	}
}
