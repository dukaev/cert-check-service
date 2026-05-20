package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dukaev/cert-check-service/internal/storage"
)

// Clock is injectable so the default `at = now` branch is testable.
type Clock interface{ Now() time.Time }

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

type Handler struct {
	Store storage.Store
	Clock Clock
}

func New(store storage.Store, clock Clock) *Handler {
	return &Handler{Store: store, Clock: clock}
}

// Response is the JSON shape returned by /api/v1/check.
// `reason` is intentionally NOT omitempty — the spec requires it as an empty string when valid.
type Response struct {
	Serial string `json:"serial"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason"`
}

// parsedRequest holds the validated query parameters.
type parsedRequest struct {
	serial string
	at     time.Time
}

// Check handles GET /api/v1/check.
// TODO(part-1): implement.
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	_, _ = parseRequest(r, h.Clock)
	writeJSON(w, http.StatusNotImplemented, Response{Reason: "TODO"})
}

// parseRequest validates and normalises query params.
// TODO(part-1): implement (delegates to parseSerial + parseAt).
func parseRequest(r *http.Request, clock Clock) (parsedRequest, error) {
	serial, _ := parseSerial(r.URL.Query().Get("serial"))
	at, _ := parseAt(r.URL.Query().Get("at"), clock)
	return parsedRequest{serial: serial, at: at}, errors.New("TODO")
}

// parseSerial validates a hex serial number.
// Rules: non-empty, even length, hex chars only ([0-9a-fA-F]). Returns the upper-cased value.
// TODO(part-1): implement.
func parseSerial(s string) (string, error) {
	return "", errors.New("TODO")
}

// parseAt validates an RFC3339 timestamp; empty string returns clock.Now().
// TODO(part-1): implement.
func parseAt(s string, clock Clock) (time.Time, error) {
	return time.Time{}, errors.New("TODO")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
