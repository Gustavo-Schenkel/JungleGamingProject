package httpserver

import (
	"errors"
	"log"
	"net/http"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/auth"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/wallet"
)

func writeFinanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, finance.ErrInvalid):
		writeJSON(w, 400, map[string]string{"error": "invalid financial operation"})
	case errors.Is(err, finance.ErrWalletNotFound):
		writeJSON(w, 404, map[string]string{"error": "wallet not found"})
	case errors.Is(err, finance.ErrWalletMismatch):
		writeJSON(w, 400, map[string]string{"error": "wallet/player/currency mismatch"})
	case errors.Is(err, finance.ErrConflict):
		writeJSON(w, 409, map[string]string{"error": "idempotency or external transaction conflict"})
	case errors.Is(err, finance.ErrTransactionNotFound):
		writeJSON(w, 404, map[string]string{"error": "transaction not found"})
	case errors.Is(err, wallet.ErrExists):
		writeJSON(w, 409, map[string]string{"error": "wallet already exists"})
	default:
		log.Printf("provider financial operation: %v", err)
		writeJSON(w, 500, map[string]string{"error": "internal error"})
	}
}

// RegisterProviderRoutes exposes real authenticated wagering endpoints.
// providerId comes from the verified access token's azp allowlist mapping.
// The JSON field may be omitted; when supplied it must match token identity.
func RegisterProviderRoutes(mux *http.ServeMux, service *finance.Service, verifier *auth.Verifier) {
	mux.HandleFunc("POST /v1/transactions", func(w http.ResponseWriter, r *http.Request) {
		verifier.Authenticated(w, r, func(p auth.Principal) {
			if p.IsInternal || p.ProviderID == "" {
				writeJSON(w, 403, map[string]string{"error": "provider client required"})
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				writeJSON(w, 400, map[string]string{"error": "Idempotency-Key header required"})
				return
			}
			var cmd finance.Command
			if err := decodeStrict(w, r, &cmd); err != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
				return
			}
			if cmd.ProviderID != "" && cmd.ProviderID != p.ProviderID {
				writeJSON(w, 403, map[string]string{"error": "provider identity mismatch"})
				return
			}
			cmd.ProviderID = p.ProviderID
			result, err := service.Process(r.Context(), cmd, key)
			if err != nil {
				writeFinanceError(w, err)
				return
			}
			status := http.StatusOK
			if result.Status == "REJECTED" {
				status = http.StatusUnprocessableEntity
			}
			writeJSON(w, status, result)
		})
	})
	mux.HandleFunc("GET /v1/transactions/{externalTransactionId}", func(w http.ResponseWriter, r *http.Request) {
		verifier.Authenticated(w, r, func(p auth.Principal) {
			if p.IsInternal || p.ProviderID == "" {
				writeJSON(w, 403, map[string]string{"error": "provider client required"})
				return
			}
			result, err := service.GetByExternalID(r.Context(), p.ProviderID, r.PathValue("externalTransactionId"))
			if err != nil {
				writeFinanceError(w, err)
				return
			}
			writeJSON(w, 200, result)
		})
	})
}
