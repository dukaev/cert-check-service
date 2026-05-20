package handler

import "net/http"

// jsonContentType is reused on every write to avoid the per-request
// `[]string{value}` slice literal that http.Header.Set would otherwise allocate.
// "Content-Type" is already in canonical MIME form so direct map assignment is safe.
var jsonContentType = []string{"application/json; charset=utf-8"}

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
