package handler

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dukaev/cert-check-service/internal/checker"
	"github.com/dukaev/cert-check-service/internal/storage"
)

// MaxSerialHexLen — RFC 5280 §4.1.2.2: serialNumber MUST NOT be longer than 20 octets.
// We accept up to 40 hex characters; anything longer is rejected at the boundary.
const MaxSerialHexLen = 40

// Clock is injectable so the default `at = now` branch is testable.
type Clock interface{ Now() time.Time }

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

type Handler struct {
	Store   storage.Store
	Readier storage.Readier // optional; if nil, /readyz returns 200
	Clock   Clock
}

func New(store storage.Store, clock Clock) *Handler {
	h := &Handler{Store: store, Clock: clock}
	// MemoryStore (and any Store impl that also implements Readier) doubles
	// as the readiness probe — avoids passing the same dependency twice.
	if r, ok := store.(storage.Readier); ok {
		h.Readier = r
	}
	return h
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

// Ready handles GET /readyz. Returns 200 only if the underlying storage
// answers Ping within the request context. /healthz (liveness) is a separate
// concern: it's 200 as long as the process is alive.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if h.Readier == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	if err := h.Readier.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "not ready: " + err.Error()})
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

// parseRequest validates and normalises query params.
func parseRequest(r *http.Request, clock Clock) (parsedRequest, error) {
	q := r.URL.Query()

	caID, err := parseCaID(q.Get("ca_id"))
	if err != nil {
		return parsedRequest{}, fmt.Errorf("ca_id: %w", err)
	}
	serial, err := parseSerial(q.Get("serial"))
	if err != nil {
		return parsedRequest{}, fmt.Errorf("serial: %w", err)
	}
	at, err := parseAt(q.Get("at"), clock)
	if err != nil {
		return parsedRequest{}, fmt.Errorf("at: %w", err)
	}
	return parsedRequest{caID: caID, serial: serial, at: at}, nil
}

// parseCaID accepts a decimal uint16. Empty defaults to 0.
// Serial is unique only within a CA, so production deployments will require
// this; default 0 keeps backward compatibility with single-CA setups.
func parseCaID(s string) (uint16, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("not a uint16: %w", err)
	}
	return uint16(n), nil
}

// parseSerial validates a hex serial number and returns it decoded to raw bytes.
// Rules: non-empty, even length, hex chars only, ≤ 40 hex chars (RFC 5280 §4.1.2.2).
func parseSerial(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("required")
	}
	if len(s) > MaxSerialHexLen {
		return nil, fmt.Errorf("too long: %d chars (max %d, RFC 5280)", len(s), MaxSerialHexLen)
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
