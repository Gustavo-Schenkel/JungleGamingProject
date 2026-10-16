package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"go.uber.org/fx"
)

func TestOIDCStartupRecoversFromTransientDiscoveryFailure(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/jungle/.well-known/openid-configuration" {
			if attempts.Add(1) == 1 {
				http.Error(w, "temporarily unavailable", 503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": server.URL + "/realms/jungle", "jwks_uri": server.URL + "/keys"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
			"kty": "RSA", "kid": "test", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	defer server.Close()
	verifier, err := NewVerifier(config.Config{Environment: "development", OIDCIssuer: server.URL + "/realms/jungle", OIDCAudience: "jungle-api", OIDCInternalClient: "internal", OIDCProviderClients: map[string]string{"client": "provider"}})
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(fx.NopLogger, fx.Provide(func() *Verifier { return verifier }), fx.Invoke(RegisterLifecycle))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()) })
	if attempts.Load() != 2 {
		t.Fatalf("discovery attempts=%d; want 2", attempts.Load())
	}
	if len(verifier.keys) != 1 {
		t.Fatal("startup succeeded without verified JWKS")
	}
}
