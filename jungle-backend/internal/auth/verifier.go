// Package auth validates Keycloak-issued OAuth2 access tokens locally via OIDC JWKS.
// It intentionally has no authentication bypass in development mode.
package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"go.uber.org/fx"
)

var ErrUnauthorized = errors.New("invalid or missing access token")
var ErrForbidden = errors.New("client is not authorized")

type Principal struct {
	ClientID   string
	ProviderID string
	IsInternal bool
	Subject    string
}

type Verifier struct {
	issuer          string
	audience        string
	providerClients map[string]string
	internalClient  string
	httpClient      *http.Client
	mu              sync.RWMutex
	keys            map[string]*rsa.PublicKey
	keyExpiry       time.Time
	jwksURL         string
	clock           func() time.Time
}

func NewVerifier(cfg config.Config) (*Verifier, error) {
	if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" || cfg.OIDCInternalClient == "" || len(cfg.OIDCProviderClients) == 0 {
		return nil, fmt.Errorf("OIDC configuration missing: issuer, audience, internal client, and provider map are mandatory")
	}
	parsed, err := url.Parse(cfg.OIDCIssuer)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("OIDC_ISSUER must be an absolute URL without credentials or query")
	}
	if parsed.Scheme != "https" && !(cfg.Environment == "development" && parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1")) {
		return nil, fmt.Errorf("OIDC issuer requires HTTPS (HTTP only allowed on localhost in development)")
	}
	return &Verifier{issuer: strings.TrimSuffix(cfg.OIDCIssuer, "/"), audience: cfg.OIDCAudience,
		providerClients: cfg.OIDCProviderClients, internalClient: cfg.OIDCInternalClient,
		httpClient: &http.Client{Timeout: 5 * time.Second}, clock: time.Now}, nil
}

func RegisterLifecycle(lc fx.Lifecycle, v *Verifier) {
	lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if err = v.refresh(ctx); err == nil {
				return nil
			}
			if attempt < 2 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
		return fmt.Errorf("OIDC startup after three attempts: %w", err)
	}})
}

type discovery struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}
type jwkSet struct {
	Keys []struct {
		Kty string   `json:"kty"`
		Use string   `json:"use"`
		Alg string   `json:"alg"`
		Kid string   `json:"kid"`
		N   string   `json:"n"`
		E   string   `json:"e"`
		X5C []string `json:"x5c"`
	} `json:"keys"`
}

func (v *Verifier) getJSON(ctx context.Context, address string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d from OIDC endpoint", resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	return dec.Decode(target)
}
func (v *Verifier) refresh(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	// Serialize refreshes. Caller may have observed expiry just before lock.
	var d discovery
	if err := v.getJSON(ctx, v.issuer+"/.well-known/openid-configuration", &d); err != nil {
		return fmt.Errorf("discovery: %w", err)
	}
	if d.Issuer != v.issuer {
		return fmt.Errorf("discovery issuer mismatch")
	}
	jwksURL, err := url.Parse(d.JWKSURI)
	issuerURL, _ := url.Parse(v.issuer)
	if err != nil || jwksURL.Host != issuerURL.Host || jwksURL.Scheme != issuerURL.Scheme || jwksURL.User != nil || jwksURL.Fragment != "" || jwksURL.RawQuery != "" {
		return fmt.Errorf("untrusted JWKS endpoint")
	}
	var doc jwkSet
	if err := v.getJSON(ctx, d.JWKSURI, &doc); err != nil {
		return fmt.Errorf("jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		exponent := new(big.Int).SetBytes(eBytes)
		if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > 1<<31-1 || exponent.Int64()%2 == 0 {
			continue
		}
		key := &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(exponent.Int64())}
		if key.N.BitLen() < 2048 {
			continue
		}
		// Reject inconsistent x5c keys when an x509 certificate is supplied.
		if len(k.X5C) > 0 {
			certDER, decodeErr := base64.StdEncoding.DecodeString(k.X5C[0])
			if decodeErr != nil {
				continue
			}
			cert, parseErr := x509.ParseCertificate(certDER)
			if parseErr != nil {
				continue
			}
			rsaKey, ok := cert.PublicKey.(*rsa.PublicKey)
			if !ok || rsaKey.E != key.E || rsaKey.N.Cmp(key.N) != 0 {
				continue
			}
		}
		keys[k.Kid] = key
	}
	if len(keys) == 0 {
		return fmt.Errorf("JWKS has no usable RS256 RSA keys")
	}
	v.keys = keys
	v.keyExpiry = v.clock().Add(5 * time.Minute)
	v.jwksURL = d.JWKSURI
	return nil
}

func (v *Verifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key := v.keys[kid]
	expiry := v.keyExpiry
	v.mu.RUnlock()
	if key != nil && v.clock().Before(expiry) {
		return key, nil
	}
	if err := v.refresh(ctx); err != nil {
		return nil, ErrUnauthorized
	}
	v.mu.RLock()
	key = v.keys[kid]
	v.mu.RUnlock()
	if key == nil {
		return nil, ErrUnauthorized
	}
	return key, nil
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}
type claims struct {
	Iss string          `json:"iss"`
	Aud json.RawMessage `json:"aud"`
	Exp int64           `json:"exp"`
	Nbf int64           `json:"nbf"`
	Iat int64           `json:"iat"`
	Azp string          `json:"azp"`
	Sub string          `json:"sub"`
	Typ string          `json:"typ"`
}

func hasAudience(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return false
	}
	for _, aud := range many {
		if aud == want {
			return true
		}
	}
	return false
}

// Verify requires a valid RS256 signature, access-token type, issuer, audience,
// time claims and an allowlisted OAuth2 client. azp, not the request JSON,
// establishes provider identity.
func (v *Verifier) Verify(ctx context.Context, raw string) (Principal, error) {
	if len(raw) > 16384 {
		return Principal{}, ErrUnauthorized
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Principal{}, ErrUnauthorized
	}
	decodedHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	var h tokenHeader
	if len(decodedHeader) > 4096 || json.Unmarshal(decodedHeader, &h) != nil || h.Alg != "RS256" || h.Kid == "" {
		return Principal{}, ErrUnauthorized
	}
	key, err := v.keyFor(ctx, h.Kid)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	hashed := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, hashed[:], sig) != nil {
		return Principal{}, ErrUnauthorized
	}
	decodedClaims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(decodedClaims) > 8192 {
		return Principal{}, ErrUnauthorized
	}
	var c claims
	if json.Unmarshal(decodedClaims, &c) != nil {
		return Principal{}, ErrUnauthorized
	}
	now := v.clock().Unix()
	if c.Iss != v.issuer || !hasAudience(c.Aud, v.audience) || c.Exp <= now || c.Exp > now+86400 || c.Nbf > now+30 || c.Iat > now+30 || c.Sub == "" || c.Typ != "Bearer" {
		return Principal{}, ErrUnauthorized
	}
	if c.Azp == v.internalClient {
		return Principal{ClientID: c.Azp, IsInternal: true, Subject: c.Sub}, nil
	}
	provider, ok := v.providerClients[c.Azp]
	if !ok {
		return Principal{}, ErrForbidden
	}
	return Principal{ClientID: c.Azp, ProviderID: provider, Subject: c.Sub}, nil
}

// Authenticated extracts bearer tokens strictly; ignores cookies and query values.
func (v *Verifier) Authenticated(w http.ResponseWriter, r *http.Request, next func(Principal)) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || len(header) <= 7 || strings.ContainsAny(header[7:], " \t\r\n") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="jungle"`)
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	p, err := v.Verify(r.Context(), strings.TrimPrefix(header, "Bearer "))
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeAuthError(w, http.StatusForbidden, "forbidden")
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	next(p)
}

func writeAuthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
