package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
)

func signedJWT(t *testing.T, key *rsa.PrivateKey, data map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	claims, _ := json.Marshal(data)
	payload := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return payload + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestOIDCVerificationProviderIsolationAndTampering(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/jungle/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": server.URL + "/realms/jungle", "jwks_uri": server.URL + "/realms/jungle/keys"})
		case "/realms/jungle/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
				"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	verifier, err := NewVerifier(config.Config{Environment: "development", OIDCIssuer: server.URL + "/realms/jungle", OIDCAudience: "jungle-api", OIDCInternalClient: "jungle-internal", OIDCProviderClients: map[string]string{"jungle-provider-alpha": "provider-alpha", "jungle-provider-beta": "provider-beta"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = verifier.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	claims := map[string]any{"iss": server.URL + "/realms/jungle", "aud": []string{"account", "jungle-api"}, "exp": now + 300, "iat": now, "azp": "jungle-provider-alpha", "sub": "user-123", "typ": "Bearer"}
	check := func(c map[string]any) (Principal, error) {
		return verifier.Verify(context.Background(), signedJWT(t, key, c))
	}
	principal, err := check(claims)
	if err != nil || principal.ProviderID != "provider-alpha" || principal.IsInternal {
		t.Fatalf("valid provider token: %+v %v", principal, err)
	}
	cloned := func() map[string]any {
		v := map[string]any{}
		for k, val := range claims {
			v[k] = val
		}
		return v
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong issuer", func(c map[string]any) { c["iss"] = "http://evil.example/realms/jungle" }},
		{"missing audience", func(c map[string]any) { c["aud"] = "account" }},
		{"expired", func(c map[string]any) { c["exp"] = now - 1 }},
		{"wrong type", func(c map[string]any) { c["typ"] = "ID" }},
		{"missing subject", func(c map[string]any) { c["sub"] = "" }},
		{"future nbf", func(c map[string]any) { c["nbf"] = now + 60 }},
		{"future iat", func(c map[string]any) { c["iat"] = now + 60 }},
		{"unapproved client", func(c map[string]any) { c["azp"] = "unauthorized-client" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := cloned()
			tc.mutate(v)
			_, e := check(v)
			if e == nil {
				t.Fatal("accepted forbidden claims")
			}
		})
	}
	invalid := signedJWT(t, key, claims)
	pieces := strings.Split(invalid, ".")
	pieces[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"changed"}`))
	if _, err = verifier.Verify(context.Background(), strings.Join(pieces, ".")); err == nil {
		t.Fatal("accepted tampered signature")
	}
	old := signedJWT(t, key, claims)
	pieces = strings.Split(old, ".")
	pieces[0] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"test-key"}`))
	if _, err = verifier.Verify(context.Background(), strings.Join(pieces, ".")); err == nil {
		t.Fatal("accepted alg none")
	}
	for _, tc := range []struct {
		client, provider string
		internal         bool
	}{
		{"jungle-provider-beta", "provider-beta", false},
		{"jungle-internal", "", true},
	} {
		v := cloned()
		v["azp"] = tc.client
		p, e := check(v)
		if e != nil || p.ProviderID != tc.provider || p.IsInternal != tc.internal {
			t.Fatalf("mapping of %s: %+v %v", tc.client, p, e)
		}
	}
	t.Run("HTTP middleware rejects missing token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		verifier.Authenticated(rec, httptest.NewRequest("GET", "/v1/transactions", nil), func(p Principal) { t.Fatal("bypassed authorization") })
		if rec.Code != 401 {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("HTTP middleware accepts valid token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/transactions", nil)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", signedJWT(t, key, claims)))
		rec := httptest.NewRecorder()
		verifier.Authenticated(rec, req, func(p Principal) {
			if p.ProviderID != "provider-alpha" {
				t.Fatal("wrong provider")
			}
			rec.WriteHeader(200)
		})
		if rec.Code != 200 {
			t.Fatalf("got %d", rec.Code)
		}
	})
}

func TestVerifierRejectsInsecureProductionIssuer(t *testing.T) {
	_, err := NewVerifier(config.Config{Environment: "production", OIDCIssuer: "http://localhost:8081/realms/jungle", OIDCAudience: "jungle-api", OIDCInternalClient: "internal", OIDCProviderClients: map[string]string{"client": "provider"}})
	if err == nil {
		t.Fatal("accepted HTTP issuer in production")
	}
}
