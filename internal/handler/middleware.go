package handler

import (
	"log/slog"
	"net/http"
	"time"
)

// AccessLog wraps a handler and emits one structured log line per request.
// Paths in `skip` (typically `/healthz`) are not logged — k8s liveness probes
// otherwise drown out real traffic.
func AccessLog(logger *slog.Logger, skip ...string) func(http.Handler) http.Handler {
	skipped := make(map[string]struct{}, len(skip))
	for _, p := range skip {
		skipped[p] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			if _, ok := skipped[r.URL.Path]; ok {
				return
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", rec.status),
				slog.Duration("dur", time.Since(start)),
			)
		})
	}
}

// statusRecorder captures the response status code so AccessLog can log it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
