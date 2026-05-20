package handler

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dukaev/cert-check-service/internal/checker"
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

// Response is the JSON shape returned by /api/v1/check on success.
// `reason` is intentionally NOT omitempty — the spec requires it as an empty string when valid.
// Serial is hex-encoded for the wire (storage layer uses []byte).
type Response struct {
	Serial string `json:"serial"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason"`
}

// ErrorResponse is the JSON body for 4xx/5xx — keeps the contract uniform
// instead of plain-text errors from http.Error.
type ErrorResponse struct {
	Error string `json:"error"`
}

// parsedRequest holds the validated query parameters.
type parsedRequest struct {
	caID   uint16
	serial []byte
	at     time.Time
}

// Check handles GET /api/v1/check.
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	req, err := parseRequest(r, h.Clock)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	// Hex-encode once for both the response and any error branch.
	serialHex := strings.ToUpper(hex.EncodeToString(req.serial))

	cert, err := h.Store.Get(r.Context(), req.caID, req.serial)
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusOK, Response{
			Serial: serialHex,
			Valid:  false,
			Reason: checker.ReasonNotFound,
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
		return
	}

	valid, reason := checker.Check(cert, req.at)
	writeJSON(w, http.StatusOK, Response{
		Serial: serialHex,
		Valid:  valid,
		Reason: reason,
	})
}

// parseRequest validates and normalises query params.
func parseRequest(r *http.Request, clock Clock) (parsedRequest, error) {
	serial, err := parseSerial(r.URL.Query().Get("serial"))
	if err != nil {
		return parsedRequest{}, fmt.Errorf("serial: %w", err)
	}
	at, err := parseAt(r.URL.Query().Get("at"), clock)
	if err != nil {
		return parsedRequest{}, fmt.Errorf("at: %w", err)
	}
	// caID is currently fixed at 0 — the public API does not expose it (spec).
	// The internal Store call is already caID-aware so adding ?ca_id= is a one-liner later.
	return parsedRequest{caID: 0, serial: serial, at: at}, nil
}

// parseSerial validates a hex serial number and returns it decoded to raw bytes.
// Rules: non-empty, even length, hex chars only.
func parseSerial(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("required")
	}
	if len(s)%2 != 0 {
		return nil, errors.New("odd length")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("not hex: %w", err)
	}
	return b, nil
}

// parseAt validates an RFC3339 timestamp; empty string returns clock.Now().
func parseAt(s string, clock Clock) (time.Time, error) {
	if s == "" {
		return clock.Now(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("not RFC3339: %w", err)
	}
	return t, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
