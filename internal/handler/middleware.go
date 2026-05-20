package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// RequestIDHeader is the wire name for the propagated request ID.
const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// RequestID returns the propagated request ID from context, or "" if absent.
// Useful for adding the ID to log lines from deeper code.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

// WithRequestID is the middleware that ensures every request has an ID:
//   - if the client passes X-Request-ID, we propagate it (idempotent retries
//     across services share the same ID — Phase 2 tracing relies on this)
//   - otherwise we generate a 16-hex-char random ID
//
// The ID is stored in context, echoed back via the response header, and made
// available to downstream middleware (notably AccessLog logs it).
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is so unusual we degrade rather than fail.
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// AccessLog wraps a handler and emits one structured log line per request.
// Paths in `skip` (typically `/healthz`) are not logged — k8s liveness probes
// otherwise drown out real traffic.
//
// If a request_id is present in context (via WithRequestID), it is attached
// to the log line — Phase 2 distributed tracing pivots on this field.
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
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", rec.status),
				slog.Duration("dur", time.Since(start)),
			}
			if id := RequestID(r.Context()); id != "" {
				attrs = append(attrs, slog.String("request_id", id))
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
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
