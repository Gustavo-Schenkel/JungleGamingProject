package messaging

import (
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/money"
	"testing"
)

func testCfg() config.Config {
	return config.Config{
		SQSCommandSecrets:   map[string]string{"alpha": "this-is-a-strong-independent-hmac-key-for-alpha-12345", "beta": "this-is-a-strong-independent-hmac-key-for-beta-12345"},
		OIDCProviderClients: map[string]string{"alpha": "provider-alpha", "beta": "provider-beta"},
	}
}
func testEnvelope() SignedCommand {
	return SignedCommand{SchemaVersion: 1, ClientID: "alpha", IdempotencyKey: "key-123",
		Command: finance.Command{WalletID: "f15b8b80-c2f2-4b99-8867-c8997ef89d62", ExternalTransactionID: "ext-1", PlayerID: "player",
			Kind: "BET", RoundID: "round", GameID: "game", Money: money.JSONMoney{Amount: "12.00", Currency: "BRL"}}}
}
func TestSignedQueueIdentityAndTampering(t *testing.T) {
	cfg := testCfg()
	e, err := SignCommand(cfg, testEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	cmd, key, err := VerifyCommand(cfg, e)
	if err != nil || cmd.ProviderID != "provider-alpha" || key != "key-123" {
		t.Fatalf("unexpected identity: %+v %q %v", cmd, key, err)
	}
	tampered := e
	tampered.Command.Money.Amount = "99.00"
	if _, _, err = VerifyCommand(cfg, tampered); err == nil {
		t.Fatal("tampered amount accepted")
	}
	tampered = e
	tampered.ClientID = "beta"
	if _, _, err = VerifyCommand(cfg, tampered); err == nil {
		t.Fatal("provider client spoof accepted")
	}
	tampered = e
	tampered.Command.ProviderID = "provider-beta"
	if _, _, err = VerifyCommand(cfg, tampered); err == nil {
		t.Fatal("forged providerId accepted")
	}
	tampered = e
	tampered.Signature = "00"
	if _, _, err = VerifyCommand(cfg, tampered); err == nil {
		t.Fatal("short HMAC accepted")
	}
}
func TestEnvelopeEncodingAndBackoff(t *testing.T) {
	if got := nextBackoff(0); got != 2 {
		t.Fatal(got)
	}
	if got := nextBackoff(30); got != 256 {
		t.Fatal(got)
	}
	if _, err := DecodeCommand(make([]byte, 32769)); err == nil {
		t.Fatal("oversized SQS command accepted")
	}
}
