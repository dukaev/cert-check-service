package handler_test

import (
	"bytes"
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
