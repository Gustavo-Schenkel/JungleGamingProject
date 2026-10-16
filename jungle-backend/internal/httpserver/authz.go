package httpserver

import (
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/auth"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"net/http"
)

// Internal wallet management is never available to provider identities.
// The development-only check is additional protection, not authentication.
func internalOnly(cfg config.Config, verifier *auth.Verifier, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		verifier.Authenticated(w, r, func(p auth.Principal) {
			if !p.IsInternal {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "internal client required"})
				return
			}
			devOnly(cfg, next)(w, r)
		})
	}
}
