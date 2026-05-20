package handler

import (
	"testing"
	"time"
)

// FuzzParseSerial: parseSerial must never panic, regardless of input.
// Run locally: go test -fuzz=FuzzParseSerial -fuzztime=30s ./internal/handler/...
func FuzzParseSerial(f *testing.F) {
	seeds := []string{"", "01A2B3", "ZZ", "1", "0xFF", "deadbeef", "01 02"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("parseSerial(%q) panicked: %v", in, r)
			}
		}()
		_, _ = parseSerial(in)
	})
}

// FuzzParseAt: parseAt must never panic. time.Parse has historically been a fuzz target.
// Run locally: go test -fuzz=FuzzParseAt -fuzztime=30s ./internal/handler/...
func FuzzParseAt(f *testing.F) {
	seeds := []string{
		"",
		"2026-01-01T00:00:00Z",
		"2026-01-01T00:00:00+03:00",
		"2026-01-01T00:00:00",
		"2026-13-40T99:99:99Z",
		"not-a-date",
		"1700000000",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	clock := stubClock{t: time.Unix(0, 0).UTC()}
	f.Fuzz(func(t *testing.T, in string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("parseAt(%q) panicked: %v", in, r)
			}
		}()
		_, _ = parseAt(in, clock)
	})
}
