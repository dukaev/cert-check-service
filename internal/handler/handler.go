package handler

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// jsonContentType is reused on every write to avoid the per-request
// `[]string{value}` slice literal that http.Header.Set would otherwise allocate.
// "Content-Type" is already in canonical MIME form so direct map assignment is safe.
var jsonContentType = []string{"application/json; charset=utf-8"}

// maxSerialBytes bounds the on-stack hex decode buffer. RFC 5280 §4.1.2.2
// caps CA serial numbers at 20 octets — 64 leaves comfortable headroom.
const maxSerialBytes = 64

// Check handles GET /api/v1/check.
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

// parseQueryFast extracts the "serial" and "at" values from a raw query string
// in a single pass without building the full url.Values map. Mirrors
// url.ParseQuery semantics: takes the first occurrence of each key and
// QueryUnescapes the value only when it contains '%' or '+'.
func parseQueryFast(raw string) (serial, at string, err error) {
	for raw != "" {
		var pair string
		if cut, rest, ok := strings.Cut(raw, "&"); ok {
			pair, raw = cut, rest
		} else {
			pair, raw = raw, ""
		}
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		switch key {
		case "serial":
			if serial != "" {
				continue
			}
			serial, err = maybeUnescape(value)
			if err != nil {
				return "", "", fmt.Errorf("serial: %w", err)
			}
		case "at":
			if at != "" {
				continue
			}
			at, err = maybeUnescape(value)
			if err != nil {
				return "", "", fmt.Errorf("at: %w", err)
			}
		}
	}
	return serial, at, nil
}

// maybeUnescape avoids calling url.QueryUnescape when the value contains no
// percent- or plus-encoded bytes — the common case for hex serials and
// RFC3339-with-Z timestamps. The escape path remains for clients that
// percent-encode the offset sign in `at=...+03:00`.
func maybeUnescape(s string) (string, error) {
	if strings.IndexByte(s, '%') < 0 && strings.IndexByte(s, '+') < 0 {
		return s, nil
	}
	return url.QueryUnescape(s)
}

// parseAtFast is the hot-path equivalent of parseAt: empty → clock.Now,
// otherwise strict RFC3339.
func parseAtFast(s string, clock Clock) (time.Time, error) {
	if s == "" {
		return clock.Now(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("not RFC3339: %w", err)
	}
	return t, nil
}

// decodeHexInto decodes a hex string into dst and returns the byte count.
// Caller guarantees len(dst) >= len(s)/2.
func decodeHexInto(dst []byte, s string) (int, error) {
	if len(s)%2 != 0 {
		return 0, errors.New("odd length")
	}
	for i := 0; i < len(s); i += 2 {
		hi, ok1 := fromHexChar(s[i])
		lo, ok2 := fromHexChar(s[i+1])
		if !ok1 || !ok2 {
			return 0, errors.New("not hex")
		}
		dst[i/2] = hi<<4 | lo
	}
	return len(s) / 2, nil
}

func fromHexChar(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

const hexAlphabetUpper = "0123456789ABCDEF"

// encodeHexUpper writes hex(src) in upper-case into dst. Caller guarantees
// len(dst) >= 2*len(src).
func encodeHexUpper(dst, src []byte) {
	for i, b := range src {
		dst[i*2] = hexAlphabetUpper[b>>4]
		dst[i*2+1] = hexAlphabetUpper[b&0x0F]
	}
}

// writeCheckResponse serialises the success/not_found JSON shape directly into
// a stack buffer, bypassing encoding/json's encoder, reflection, and the
// interface-boxing of `any` parameters. The wire shape is locked by
// TestCheck_ResponseShape_Golden.
func writeCheckResponse(w http.ResponseWriter, serialHex []byte, valid bool, reason string) {
	var buf [256]byte
	out := buf[:0]
	out = append(out, `{"serial":"`...)
	out = append(out, serialHex...)
	out = append(out, `","valid":`...)
	if valid {
		out = append(out, `true`...)
	} else {
		out = append(out, `false`...)
	}
	out = append(out, `,"reason":"`...)
	out = append(out, reason...) // reason ∈ {"", expired, revoked, not_found} — safe to embed raw
	out = append(out, '"', '}', '\n')

	h := w.Header()
	h["Content-Type"] = jsonContentType
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// writeJSONError writes {"error":"..."} with the given status. msg may contain
// arbitrary bytes (it wraps user-supplied data through fmt.Errorf), so it is
// JSON-escaped via appendJSONString.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	var buf [512]byte
	out := buf[:0]
	out = append(out, `{"error":`...)
	out = appendJSONString(out, msg)
	out = append(out, '}', '\n')

	h := w.Header()
	h["Content-Type"] = jsonContentType
	w.WriteHeader(status)
	_, _ = w.Write(out)
}

// appendJSONString appends a JSON-quoted string to buf. Bytes ≥ 0x80 pass
// through unchanged (valid UTF-8 is allowed inside JSON strings).
func appendJSONString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\':
			buf = append(buf, '\\', c)
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		default:
			if c < 0x20 {
				buf = append(buf, '\\', 'u', '0', '0',
					hexAlphabetLower[c>>4], hexAlphabetLower[c&0x0F])
			} else {
				buf = append(buf, c)
			}
		}
	}
	return append(buf, '"')
}

const hexAlphabetLower = "0123456789abcdef"

// --- helpers kept for the parse_test.go unit suite --------------------------

// parseSerial validates a hex serial number and returns it decoded to raw bytes.
// Hot-path callers use decodeHexInto directly to avoid the per-call allocation.
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
	return parseAtFast(s, clock)
}
