package handler

import "testing"

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
