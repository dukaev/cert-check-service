package handler

import (
	"testing"
	"time"
)

type stubClock struct{ t time.Time }

func (s stubClock) Now() time.Time { return s.t }

func TestParseSerial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty", "", "", true},
		{"non-hex letter", "01XZ", "", true},
		{"space inside", "01 02", "", true},
		{"0x prefix not allowed", "0x01", "", true},
		{"odd length", "1A2", "", true},
		{"lowercase normalized to upper", "01a2b3", "01A2B3", false},
		{"uppercase preserved", "01A2B3", "01A2B3", false},
		{"long hex", "abcdef0123456789", "ABCDEF0123456789", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSerial(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseSerial(%q) err=nil, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Errorf("parseSerial(%q) err=%v, want nil", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseSerial(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseAt(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	clock := stubClock{t: fixed}

	t.Run("empty → clock.Now()", func(t *testing.T) {
		got, err := parseAt("", clock)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(fixed) {
			t.Errorf("got %v, want %v", got, fixed)
		}
	})

	t.Run("valid RFC3339 Z", func(t *testing.T) {
		got, err := parseAt("2026-01-01T00:00:00Z", clock)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("valid RFC3339 with offset", func(t *testing.T) {
		_, err := parseAt("2026-01-01T00:00:00+03:00", clock)
		if err != nil {
			t.Errorf("want nil err for +03:00, got %v", err)
		}
	})

	for _, bad := range []string{"2026-01-01", "yesterday", "1700000000", "Mon, 01 Jan 2026"} {
		t.Run("invalid: "+bad, func(t *testing.T) {
			if _, err := parseAt(bad, clock); err == nil {
				t.Errorf("parseAt(%q) err=nil, want error", bad)
			}
		})
	}
}
