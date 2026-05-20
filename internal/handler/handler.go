package handler

import (
	"errors"
	"net/http"
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

// Check handles GET /api/v1/check.
//
// Hot path is split across:
//   - parse.go    — query string + serial/at validation
//   - hex.go      — stack-buffered hex decode/encode
//   - response.go — hand-rolled JSON writers (no encoding/json on the wire path)
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	serialStr, atStr, qErr := parseQueryFast(r.URL.RawQuery)
	if qErr != nil {
		writeJSONError(w, http.StatusBadRequest, qErr.Error())
		return
	}

	if serialStr == "" {
		writeJSONError(w, http.StatusBadRequest, "serial: required")
		return
	}
	if len(serialStr) > 2*maxSerialBytes {
		writeJSONError(w, http.StatusBadRequest, "serial: too long")
		return
	}

	// Decode into a stack buffer. The slice may still escape via the Store
	// interface call, but the array does not need to be heap-allocated up front.
	var serialBuf [maxSerialBytes]byte
	n, derr := decodeHexInto(serialBuf[:], serialStr)
	if derr != nil {
		writeJSONError(w, http.StatusBadRequest, "serial: "+derr.Error())
		return
	}
	serial := serialBuf[:n]

	at, aerr := parseAtFast(atStr, h.Clock)
	if aerr != nil {
		writeJSONError(w, http.StatusBadRequest, "at: "+aerr.Error())
		return
	}

	cert, err := h.Store.Get(r.Context(), 0, serial)

	// Hex-echo the serial in upper case for the response. Encoded into a stack
	// buffer to avoid the two-allocation `strings.ToUpper(hex.EncodeToString(...))` pair.
	var hexBuf [2 * maxSerialBytes]byte
	encodeHexUpper(hexBuf[:2*n], serial)
	serialHex := hexBuf[:2*n]

	if errors.Is(err, storage.ErrNotFound) {
		writeCheckResponse(w, serialHex, false, checker.ReasonNotFound)
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	valid, reason := checker.Check(cert, at)
	writeCheckResponse(w, serialHex, valid, reason)
}
