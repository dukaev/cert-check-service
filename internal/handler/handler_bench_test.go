package handler_test

import (
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/handler"
	"github.com/dukaev/cert-check-service/internal/model"
)

// Run: go test -bench=. -benchmem -benchtime=5s ./internal/handler/...
func BenchmarkHandler_Check(b *testing.B) {
	serial, _ := hex.DecodeString("01A2B3")
	store := &fakeStore{data: map[string]model.Certificate{
		string(serial): {Serial: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)},
	}}
	h := handler.New(store, handler.RealClock{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/check", h.Check)

	req := httptest.NewRequest("GET", "/api/v1/check?serial=01A2B3", nil)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
		}
	})
}

// noopResponseWriter is a minimal ResponseWriter that discards the body and
// keeps a reusable Header map. It strips out the httptest.NewRecorder /
// Header.Clone overhead so the benchmark reflects allocations attributable to
// the handler itself, not the test harness.
type noopResponseWriter struct {
	hdr http.Header
}

func (n *noopResponseWriter) Header() http.Header {
	if n.hdr == nil {
		n.hdr = make(http.Header, 2)
	}
	return n.hdr
}
func (n *noopResponseWriter) Write(p []byte) (int, error) { return io.Discard.Write(p) }
func (n *noopResponseWriter) WriteHeader(int)             {}

// BenchmarkHandler_Check_NoopWriter measures the handler in isolation,
// without httptest.ResponseRecorder's per-call alloc tax.
func BenchmarkHandler_Check_NoopWriter(b *testing.B) {
	serial, _ := hex.DecodeString("01A2B3")
	store := &fakeStore{data: map[string]model.Certificate{
		string(serial): {Serial: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)},
	}}
	h := handler.New(store, handler.RealClock{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/check", h.Check)

	req := httptest.NewRequest("GET", "/api/v1/check?serial=01A2B3", nil)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := &noopResponseWriter{}
		for pb.Next() {
			mux.ServeHTTP(w, req)
		}
	})
}
