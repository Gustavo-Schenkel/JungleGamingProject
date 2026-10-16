package httpserver

import (
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/auth"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"net/http"
)

// Authenticated internal diagnostics; no unauthenticated queue state leak.
func RegisterMessagingRoutes(mux *http.ServeMux, db *pgxpool.Pool, cfg config.Config, v *auth.Verifier) {
	mux.HandleFunc("GET /internal/dev/messaging/status", internalOnly(cfg, v, func(w http.ResponseWriter, r *http.Request) {
		var pending, inFlight, published, inbox int64
		err := db.QueryRow(r.Context(), `SELECT outbox_pending,outbox_in_flight,outbox_published,inbox_processed
   FROM stage7_messaging_status`).Scan(&pending, &inFlight, &published, &inbox)
		if err != nil {
			log.Printf("messaging status: %v", err)
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, 200, map[string]any{"outboxPending": pending, "outboxInFlight": inFlight, "outboxPublished": published, "inboxProcessed": inbox})
	}))
}
