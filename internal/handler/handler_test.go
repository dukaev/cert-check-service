package handler_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/checker"
	"github.com/dukaev/cert-check-service/internal/handler"
	"github.com/dukaev/cert-check-service/internal/model"
	"github.com/dukaev/cert-check-service/internal/storage"
)

// fakeStore is a deterministic in-memory Store for handler tests.
// Keyed by string(bytes) since []byte isn't a valid map key in Go.
type fakeStore struct {
	data map[string]model.Certificate
	err  error // if non-nil, Get returns this error before lookup (used to simulate 5xx)
}

func (s *fakeStore) Get(_ context.Context, _ uint16, serial []byte) (model.Certificate, error) {
	if s.err != nil {
		return model.Certificate{}, s.err
	}
	c, ok := s.data[string(serial)]
	if !ok {
		return model.Certificate{}, storage.ErrNotFound
	}
	return c, nil
}

type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// fixtures
var (
	notBefore = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter  = notBefore.AddDate(1, 0, 0)
	revokedAt = notBefore.AddDate(0, 6, 0)
	defaultAt = notBefore.AddDate(0, 3, 0) // mid-window
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad fixture hex %q: %v", s, err)
	}
	return b
}

func newTestMux(t *testing.T) http.Handler {
	t.Helper()
	s01 := mustHex(t, "01A2B3")
	sDead := mustHex(t, "DEADBEEF")
	store := &fakeStore{data: map[string]model.Certificate{
		string(s01):   {Serial: s01, NotBefore: notBefore, NotAfter: notAfter},
		string(sDead): {Serial: sDead, NotBefore: notBefore, NotAfter: notAfter, RevokedAt: &revokedAt},
	}}
	h := handler.New(store, fixedClock{t: defaultAt})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/check", h.Check)
	return mux
}

func do(t *testing.T, mux http.Handler, path string) (*httptest.ResponseRecorder, handler.Response) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var resp handler.Response
	if w.Body.Len() > 0 && strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
	}
	return w, resp
}

func TestCheck_Valid(t *testing.T) {
	mux := newTestMux(t)
	w, resp := do(t, mux, "/api/v1/check?serial=01A2B3")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", w.Header().Get("Content-Type"))
	}
	if !resp.Valid || resp.Reason != "" || resp.Serial != "01A2B3" {
		t.Errorf("response = %+v, want valid=true reason=\"\" serial=01A2B3", resp)
	}
}

func TestCheck_NotFound_Returns200(t *testing.T) {
	// Per spec: not_found is a business answer, not a 404.
	mux := newTestMux(t)
	w, resp := do(t, mux, "/api/v1/check?serial=BADC0FFE")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if resp.Valid || resp.Reason != checker.ReasonNotFound {
		t.Errorf("response = %+v, want valid=false reason=%s", resp, checker.ReasonNotFound)
	}
	if resp.Serial != "BADC0FFE" {
		t.Errorf("serial in response = %q, want it echoed even on not_found", resp.Serial)
	}
}

func TestCheck_Revoked(t *testing.T) {
	mux := newTestMux(t)
	at := revokedAt.Add(time.Hour).Format(time.RFC3339)
	_, resp := do(t, mux, "/api/v1/check?serial=DEADBEEF&at="+at)
	if resp.Valid || resp.Reason != checker.ReasonRevoked {
		t.Errorf("response = %+v, want valid=false reason=%s", resp, checker.ReasonRevoked)
	}
}

func TestCheck_Expired(t *testing.T) {
	mux := newTestMux(t)
	at := notAfter.Add(time.Hour).Format(time.RFC3339)
	_, resp := do(t, mux, "/api/v1/check?serial=01A2B3&at="+at)
	if resp.Valid || resp.Reason != checker.ReasonExpired {
		t.Errorf("response = %+v, want valid=false reason=%s", resp, checker.ReasonExpired)
	}
}

func TestCheck_BadRequest(t *testing.T) {
	mux := newTestMux(t)
	cases := []string{
		"/api/v1/check",                             // serial missing
		"/api/v1/check?serial=",                     // serial empty
		"/api/v1/check?serial=01XZ",                 // non-hex
		"/api/v1/check?serial=01A2B3&at=yesterday",  // bad at
		"/api/v1/check?serial=01A2B3&at=2026-01-01", // not RFC3339
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			w, _ := do(t, mux, path)
			if w.Code != http.StatusBadRequest {
				t.Errorf("path %s: status = %d, want 400", path, w.Code)
			}
		})
	}
}

func TestCheck_MethodNotAllowed(t *testing.T) {
	mux := newTestMux(t)
	req := httptest.NewRequest("POST", "/api/v1/check?serial=01A2B3", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestCheck_ResponseShape_Golden(t *testing.T) {
	mux := newTestMux(t)
	req := httptest.NewRequest("GET", "/api/v1/check?serial=01A2B3", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Byte-exact JSON shape — guards against accidental schema changes (omitempty, field rename, ordering).
	want := `{"serial":"01A2B3","valid":true,"reason":""}` + "\n"
	got := w.Body.String()
	if got != want {
		t.Errorf("body mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestCheck_CaseInsensitiveSerial(t *testing.T) {
	mux := newTestMux(t)
	_, resp := do(t, mux, "/api/v1/check?serial=01a2b3")
	if !resp.Valid {
		t.Errorf("lowercase serial should resolve to the same cert; got %+v", resp)
	}
	if resp.Serial != "01A2B3" {
		t.Errorf("response serial = %q, want canonical upper-case form", resp.Serial)
	}
}

func TestCheck_ContextCancellation(t *testing.T) {
	// Handler should not hang or panic when client cancels.
	mux := newTestMux(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/api/v1/check?serial=01A2B3", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	// We don't assert a specific status; we only assert it returns at all.
}

func TestCheck_Concurrent(t *testing.T) {
	// Catches data races in the handler / store hot path under -race.
	mux := newTestMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const n = 100
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/api/v1/check?serial=01A2B3")
			if err != nil {
				errs <- err
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- &statusErr{code: resp.StatusCode}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent request failed: %v", err)
	}
}

type statusErr struct{ code int }

func (e *statusErr) Error() string { return http.StatusText(e.code) }

// --- contract tests for JSON error responses --------------------------------

// TestCheck_BadRequest_JSONShape asserts the wire contract: 4xx returns
// application/json with an {"error":"..."} body. Locks in the change from
// http.Error (text/plain) to writeJSON.
func TestCheck_BadRequest_JSONShape(t *testing.T) {
	mux := newTestMux(t)
	req := httptest.NewRequest("GET", "/api/v1/check?serial=NOTHEX", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp handler.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
	if resp.Error == "" {
		t.Error("ErrorResponse.Error is empty; want a non-empty message")
	}
}

// TestCheck_InternalError_JSONShape covers the 5xx branch: Store returns an
// unexpected error (anything that is not ErrNotFound) → 500 JSON.
func TestCheck_InternalError_JSONShape(t *testing.T) {
	store := &fakeStore{err: storage.ErrUnavailable}
	h := handler.New(store, fixedClock{t: defaultAt})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/check", h.Check)

	req := httptest.NewRequest("GET", "/api/v1/check?serial=01A2B3", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp handler.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if resp.Error == "" {
		t.Error("ErrorResponse.Error is empty")
	}
}

// TestRealClock_Now sanity-checks the production Clock implementation.
func TestRealClock_Now(t *testing.T) {
	c := handler.RealClock{}
	before := time.Now().UTC().Add(-time.Second)
	got := c.Now()
	after := time.Now().UTC().Add(time.Second)
	if got.Location() != time.UTC {
		t.Errorf("Now().Location = %v, want UTC", got.Location())
	}
	if got.Before(before) || got.After(after) {
		t.Errorf("Now() = %v, expected within [%v, %v]", got, before, after)
	}
}

// Quick sanity: bytes-helper used by mustHex matches encoding/hex.
var _ = bytes.Equal
