package httpserver

import (
	"encoding/json"
	"errors"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/auth"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/wallet"
	"io"
	"log"
	"net/http"
	"regexp"
)

var uuidPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func RegisterWalletRoutes(mux *http.ServeMux, repo *wallet.Repository, cfg config.Config, verifier *auth.Verifier) {
	mux.HandleFunc("POST /internal/wallets", internalOnly(cfg, verifier, func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var body struct {
			PlayerID string `json:"playerId"`
			Currency string `json:"currency"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			writeJSON(w, 400, map[string]string{"error": "JSON must contain exactly one object"})
			return
		}
		result, err := repo.Create(r.Context(), body.PlayerID, body.Currency)
		if errors.Is(err, wallet.ErrInvalid) {
			writeJSON(w, 400, map[string]string{"error": "invalid playerId or currency"})
			return
		}
		if errors.Is(err, wallet.ErrExists) {
			writeJSON(w, 409, map[string]string{"error": "wallet already exists"})
			return
		}
		if err != nil {
			log.Printf("create wallet: %v", err)
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, 201, result)
	}))
	mux.HandleFunc("GET /internal/wallets/{id}", internalOnly(cfg, verifier, func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !uuidPattern.MatchString(id) {
			writeJSON(w, 400, map[string]string{"error": "invalid wallet ID"})
			return
		}
		result, err := repo.Get(r.Context(), id)
		if errors.Is(err, wallet.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "wallet not found"})
			return
		}
		if err != nil {
			log.Printf("get wallet: %v", err)
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, 200, result)
	}))
}
