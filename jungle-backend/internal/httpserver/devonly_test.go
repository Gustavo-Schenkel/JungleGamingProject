package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
)

func TestDevOnlyRejectsRemoteAndNonDev(t *testing.T) {
	callback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	testCases := []struct {
		name   string
		env    string
		addr   string
		status int
	}{
		{"local dev", "development", "127.0.0.1:54321", http.StatusNoContent},
		{"remote dev", "development", "10.0.0.1:54321", http.StatusForbidden},
		{"local production", "production", "127.0.0.1:54321", http.StatusNotFound},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/internal/dev", nil)
			req.RemoteAddr = tt.addr
			devOnly(config.Config{Environment: tt.env}, callback).ServeHTTP(recorder, req)
			if recorder.Code != tt.status {
				t.Fatalf("wanted HTTP %d, got %d", tt.status, recorder.Code)
			}
		})
	}
}
