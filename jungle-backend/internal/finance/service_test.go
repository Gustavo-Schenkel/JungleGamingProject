package finance

import (
	"errors"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/money"
	"testing"
)

func exampleCommand() Command {
	return Command{ProviderID: "provider-a", ExternalTransactionID: "bet-1", PlayerID: "player-1",
		WalletID: "00879914-6baf-4ad6-a7c4-053ab053e644", RoundID: "round-1", GameID: "game-1", Kind: "BET",
		Money: money.JSONMoney{Amount: "25.00", Currency: "BRL"}}
}
func TestHashNormalizesEquivalentAmounts(t *testing.T) {
	a := exampleCommand()
	_, h1, e := validate(a, "key-1")
	if e != nil {
		t.Fatal(e)
	}
	a.Money.Amount = "25.0"
	_, h2, e := validate(a, "key-2")
	if e != nil {
		t.Fatal(e)
	}
	if h1 != h2 {
		t.Fatal("equivalent monetary inputs must hash equally")
	}
	a.GameID = "different-game"
	_, h3, e := validate(a, "key-2")
	if e != nil {
		t.Fatal(e)
	}
	if h1 == h3 {
		t.Fatal("different payload must hash differently")
	}
}
func TestRejectsUnsupportedAndInvalidCommands(t *testing.T) {
	tests := []func(*Command){
		func(c *Command) { c.Kind = "ROLLBACK" }, // missing reference
		func(c *Command) { c.Kind = "LOSS" },     // nonzero amount
		func(c *Command) { c.Money.Amount = "1e2" },
		func(c *Command) { c.Money.Amount = "0.00" },
		func(c *Command) { c.WalletID = "not-a-uuid" },
		func(c *Command) { c.ReferenceExternalTransactionID = "missing-bet" },
	}
	for _, change := range tests {
		c := exampleCommand()
		change(&c)
		if _, _, err := validate(c, "idempotency-key"); !errors.Is(err, ErrInvalid) {
			t.Errorf("expected invalid for %+v, got %v", c, err)
		}
	}
	if _, _, err := validate(exampleCommand(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted empty idempotency key: %v", err)
	}
}

func TestValidateStage5MoneyAndReferenceRules(t *testing.T) {
	c := exampleCommand()
	c.Kind = "LOSS"
	c.Money.Amount = "0.00"
	if _, _, err := validate(c, "key"); err != nil {
		t.Fatalf("zero LOSS rejected: %v", err)
	}
	c.Kind = "REFUND"
	c.Money.Amount = "25.00"
	c.ReferenceExternalTransactionID = "bet-original"
	if _, _, err := validate(c, "key"); err != nil {
		t.Fatalf("REFUND rejected: %v", err)
	}
	c.Kind = "ROLLBACK"
	if _, _, err := validate(c, "key"); err != nil {
		t.Fatalf("ROLLBACK rejected: %v", err)
	}
	c.ReferenceExternalTransactionID = c.ExternalTransactionID
	if _, _, err := validate(c, "key"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted self-reference: %v", err)
	}
}
func TestReferenceBackoffBounded(t *testing.T) {
	if referenceBackoff(1).Seconds() != 2 || referenceBackoff(100).Seconds() != 256 {
		t.Fatal("invalid retry backoff")
	}
}
