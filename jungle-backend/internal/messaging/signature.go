// Package messaging provides authenticated SQS inbound commands, a transactional
// inbox, and a leased PostgreSQL outbox publisher. No monetary float is used.
package messaging

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
)

var ErrInvalidSignature = errors.New("unauthenticated SQS command")

type SignedCommand struct {
	SchemaVersion  int             `json:"schemaVersion"`
	ClientID       string          `json:"clientId"`
	IdempotencyKey string          `json:"idempotencyKey"`
	Command        finance.Command `json:"command"`
	Signature      string          `json:"signature"`
}

type unsignedCommand struct {
	SchemaVersion  int             `json:"schemaVersion"`
	ClientID       string          `json:"clientId"`
	IdempotencyKey string          `json:"idempotencyKey"`
	Command        finance.Command `json:"command"`
}

func (e SignedCommand) signedBytes() ([]byte, error) {
	return json.Marshal(unsignedCommand{e.SchemaVersion, e.ClientID, e.IdempotencyKey, e.Command})
}

// SignCommand is only for trusted publishers that hold a client-specific
// 256-bit signing secret. The secret itself never enters the SQS message.
func SignCommand(cfg config.Config, e SignedCommand) (SignedCommand, error) {
	if e.SchemaVersion != 1 || !validEnvelopeID(e.IdempotencyKey) {
		return SignedCommand{}, ErrInvalidSignature
	}
	secret := cfg.SQSCommandSecrets[e.ClientID]
	if len(secret) < 32 || cfg.OIDCProviderClients[e.ClientID] == "" {
		return SignedCommand{}, ErrInvalidSignature
	}
	if e.Command.ProviderID != "" && e.Command.ProviderID != cfg.OIDCProviderClients[e.ClientID] {
		return SignedCommand{}, ErrInvalidSignature
	}
	data, err := e.signedBytes()
	if err != nil {
		return SignedCommand{}, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(data)
	e.Signature = hex.EncodeToString(mac.Sum(nil))
	return e, nil
}

func validEnvelopeID(s string) bool {
	return len(s) > 0 && len(s) <= 200 && strings.TrimSpace(s) == s && !strings.ContainsRune(s, 0)
}

// VerifyCommand rejects unknown clients and provider spoofing. ProviderID is
// determined only from the server-side allowlist AFTER HMAC verification.
func VerifyCommand(cfg config.Config, e SignedCommand) (finance.Command, string, error) {
	if e.SchemaVersion != 1 || !validEnvelopeID(e.IdempotencyKey) {
		return finance.Command{}, "", ErrInvalidSignature
	}
	secret := cfg.SQSCommandSecrets[e.ClientID]
	provider := cfg.OIDCProviderClients[e.ClientID]
	if len(secret) < 32 || provider == "" {
		return finance.Command{}, "", ErrInvalidSignature
	}
	sig, err := hex.DecodeString(e.Signature)
	if err != nil || len(sig) != sha256.Size {
		return finance.Command{}, "", ErrInvalidSignature
	}
	data, err := e.signedBytes()
	if err != nil {
		return finance.Command{}, "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(data)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return finance.Command{}, "", ErrInvalidSignature
	}
	if e.Command.ProviderID != "" && e.Command.ProviderID != provider {
		return finance.Command{}, "", ErrInvalidSignature
	}
	e.Command.ProviderID = provider
	return e.Command, e.IdempotencyKey, nil
}

func DecodeCommand(data []byte) (SignedCommand, error) {
	if len(data) == 0 || len(data) > 32768 {
		return SignedCommand{}, fmt.Errorf("SQS body invalid size")
	}
	var e SignedCommand
	if err := json.Unmarshal(data, &e); err != nil {
		return e, err
	}
	return e, nil
}
