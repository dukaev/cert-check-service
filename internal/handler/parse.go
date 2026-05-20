package handler

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

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
