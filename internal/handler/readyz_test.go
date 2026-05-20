package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dukaev/cert-check-service/internal/handler"
	"github.com/dukaev/cert-check-service/internal/model"
	"github.com/dukaev/cert-check-service/internal/storage"
)

// readierStore is a Store that also satisfies storage.Readier.
type readierStore struct {
	fakeStore
	pingErr error
}

func (s *readierStore) Ping(_ context.Context) error { return s.pingErr }

func TestReady_OK_WhenPingNil(t *testing.T) {
	store := &readierStore{fakeStore: fakeStore{data: map[fakeKey]model.Certificate{}}}
	h := handler.New(store, fixedClock{t: defaultAt})

	req := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	h.Ready(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestReady_503_WhenPingFails(t *testing.T) {
	store := &readierStore{
		fakeStore: fakeStore{data: map[fakeKey]model.Certificate{}},
		pingErr:   errors.New("postgres down"),
	}
	h := handler.New(store, fixedClock{t: defaultAt})

	req := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	h.Ready(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", w.Header().Get("Content-Type"))
	}
	var resp handler.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if !strings.Contains(resp.Error, "postgres down") {
		t.Errorf("ErrorResponse.Error = %q, want to mention 'postgres down'", resp.Error)
	}
}

func TestReady_200_WhenStoreNotReadier(t *testing.T) {
	// fakeStore (without embedded readier) does not satisfy storage.Readier.
	// In that case /readyz must default to 200 (current Phase 1 semantics).
	store := &fakeStore{data: map[fakeKey]model.Certificate{}}
	h := handler.New(store, fixedClock{t: defaultAt})

	req := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	h.Ready(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// TestMemoryStore_SatisfiesReadier ensures MemoryStore is auto-wired as the
// readiness probe by handler.New — Phase 1 default and the typical Phase 2
// path (Postgres impl also satisfies Readier).
func TestMemoryStore_SatisfiesReadier(t *testing.T) {
	var _ storage.Readier = (*storage.MemoryStore)(nil)
}
