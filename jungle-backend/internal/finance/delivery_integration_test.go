package finance

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestReferenceRejectsDifferentGameIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "100.00")
	betID := "bet-" + uniqueID(t)
	stage5Run(t, s, ctx, stage5Command(w, "BET", betID, "10.00", ""))
	for _, kind := range []string{"REFUND", "ROLLBACK", "WIN"} {
		cmd := stage5Command(w, kind, "other-game-"+uniqueID(t), "10.00", betID)
		cmd.GameID = "different-game"
		got := stage5Run(t, s, ctx, cmd)
		if got.Status != "REJECTED" || got.FailureCode != "REFERENCE_MISMATCH" || got.Balance.Amount != "90.00" {
			t.Fatalf("cross-game reference accepted: %+v", got)
		}
	}
}

func TestOutboxOrderUsesInsertionNotTransactionStartIntegration(t *testing.T) {
	s, ctx := integrationService(t)
	w := createFunded(t, s, ctx, "100.00")
	early, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer early.Rollback(ctx)
	// NOW() is fixed before another transaction commits the first movement.
	var ignored string
	if err := early.QueryRow(ctx, `SELECT now()::text`).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	first := stage5Run(t, s, ctx, stage5Command(w, "BET", "later-start-"+uniqueID(t), "10.00", ""))
	cmd := stage5Command(w, "BET", "early-start-"+uniqueID(t), "10.00", "")
	second, err := s.ProcessInTx(ctx, early, cmd, "key-"+cmd.ExternalTransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := early.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var ordered bool
	err = s.DB.QueryRow(ctx, `SELECT a.delivery_sequence < b.delivery_sequence
        FROM sqs_outbox a CROSS JOIN sqs_outbox b
        WHERE a.transaction_id=$1 AND b.transaction_id=$2`, first.TransactionID, second.TransactionID).Scan(&ordered)
	if err != nil {
		t.Fatal(err)
	}
	if !ordered {
		t.Fatal("outbox events reversed wallet movement order")
	}
}
