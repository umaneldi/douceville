package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// logger renvoie le logger par défaut enrichi de l'identifiant de requête.
func logger(ctx context.Context) *slog.Logger {
	if id := middleware.GetReqID(ctx); id != "" {
		return slog.Default().With("request_id", id)
	}
	return slog.Default()
}

// requestLogger journalise chaque requête HTTP (méthode, chemin, statut, durée).
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			switch {
			case status >= 500:
				level = slog.LevelError
			case status >= 400:
				level = slog.LevelWarn
			}
			logger(r.Context()).Log(r.Context(), level, "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
			)
		}()

		next.ServeHTTP(ww, r)
	})
}
