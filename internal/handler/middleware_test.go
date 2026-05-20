package handler_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dukaev/cert-check-service/internal/handler"
)

func TestAccessLog_LogsRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := handler.AccessLog(logger, "/healthz")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest("GET", "/foo?bar=1", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	for _, want := range []string{`"method":"GET"`, `"path":"/foo"`, `"query":"bar=1"`, `"status":418`, `"dur"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log line missing %q\ngot: %s", want, out)
		}
	}
}

func TestAccessLog_SkipsHealthz(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := handler.AccessLog(logger, "/healthz")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/healthz", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if buf.Len() != 0 {
		t.Errorf("/healthz should be skipped, got: %s", buf.String())
	}
}

// AccessLog includes request_id when WithRequestID has populated context.
func TestAccessLog_IncludesRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	chain := handler.WithRequestID(handler.AccessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest("GET", "/foo", nil)
	req.Header.Set(handler.RequestIDHeader, "client-supplied-id")
	h := chain
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), `"request_id":"client-supplied-id"`) {
		t.Errorf("expected request_id in log line, got: %s", buf.String())
	}
}

func TestWithRequestID_PropagatesClientHeader(t *testing.T) {
	var seen string
	mw := handler.WithRequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = handler.RequestID(r.Context())
	}))
	req := httptest.NewRequest("GET", "/foo", nil)
	req.Header.Set(handler.RequestIDHeader, "abc-123")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if seen != "abc-123" {
		t.Errorf("context request_id = %q, want %q", seen, "abc-123")
	}
	if w.Header().Get(handler.RequestIDHeader) != "abc-123" {
		t.Errorf("response header X-Request-ID = %q, want echoed", w.Header().Get(handler.RequestIDHeader))
	}
}

func TestWithRequestID_GeneratesWhenAbsent(t *testing.T) {
	var seen string
	mw := handler.WithRequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = handler.RequestID(r.Context())
	}))
	req := httptest.NewRequest("GET", "/foo", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if seen == "" {
		t.Error("expected generated request_id, got empty")
	}
	if len(seen) != 16 {
		t.Errorf("expected 16-char hex ID, got %d chars: %q", len(seen), seen)
	}
	if w.Header().Get(handler.RequestIDHeader) != seen {
		t.Errorf("response header X-Request-ID = %q, want %q", w.Header().Get(handler.RequestIDHeader), seen)
	}
}

func TestRequestID_AbsentContext(t *testing.T) {
	if id := handler.RequestID(context.Background()); id != "" {
		t.Errorf("RequestID(empty ctx) = %q, want empty", id)
	}
}
