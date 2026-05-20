package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/handler"
	"github.com/dukaev/cert-check-service/internal/model"
)

// Run: go test -bench=. -benchmem -benchtime=5s ./internal/handler/...
func BenchmarkHandler_Check(b *testing.B) {
	store := &fakeStore{data: map[string]model.Certificate{
		"01A2B3": {Serial: "01A2B3", NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)},
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
