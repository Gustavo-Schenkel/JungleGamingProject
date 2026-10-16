package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/auth"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/wallet"
)

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// Secondary loopback/development guard for internal-only endpoints.
// JWT verification is performed separately by internalOnly.
func devOnly(cfg config.Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Environment != "development" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if !isLocalRequest(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "local development only"})
			return
		}
		next(w, r)
	}
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("multiple JSON documents")
	}
	return nil
}

func RegisterFinanceRoutes(mux *http.ServeMux, s *finance.Service, cfg config.Config, verifier *auth.Verifier) {
	mux.HandleFunc("POST /internal/dev/wallets/open", internalOnly(cfg, verifier, func(w http.ResponseWriter, r *http.Request) {
		var cmd finance.OpeningCommand
		if err := decodeStrict(w, r, &cmd); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		result, err := s.OpenWallet(r.Context(), cmd)
		if errors.Is(err, finance.ErrInvalid) {
			writeJSON(w, 400, map[string]string{"error": "invalid playerId or initialBalance"})
			return
		}
		if errors.Is(err, wallet.ErrExists) {
			writeJSON(w, 409, map[string]string{"error": "wallet already exists"})
			return
		}
		if err != nil {
			log.Printf("open wallet: %v", err)
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, 201, result)
	}))
	mux.HandleFunc("GET /internal/dev/wallets/{id}/ledger", internalOnly(cfg, verifier, func(w http.ResponseWriter, r *http.Request) {
		ledger, err := s.Ledger(r.Context(), r.PathValue("id"))
		if errors.Is(err, finance.ErrInvalid) {
			writeJSON(w, 400, map[string]string{"error": "invalid wallet ID"})
			return
		}
		if err != nil {
			log.Printf("get ledger: %v", err)
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, 200, ledger)
	}))
}
