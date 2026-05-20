package checker_test

import (
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/checker"
	"github.com/dukaev/cert-check-service/internal/model"
)

func TestCheck(t *testing.T) {
	t.Parallel()

	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := notBefore.AddDate(1, 0, 0)
	revokedAt := notBefore.AddDate(0, 6, 0)

	base := model.Certificate{
		Serial:    "01A2B3",
		NotBefore: notBefore,
		NotAfter:  notAfter,
	}
	revoked := base
	revoked.RevokedAt = &revokedAt

	tests := []struct {
		name       string
		cert       model.Certificate
		at         time.Time
		wantValid  bool
		wantReason string
	}{
		{"valid mid-window", base, notBefore.AddDate(0, 3, 0), true, ""},
		{"boundary NotBefore inclusive", base, notBefore, true, ""},
		{"boundary NotAfter inclusive", base, notAfter, true, ""},
		{"expired before NotBefore", base, notBefore.Add(-time.Second), false, checker.ReasonExpired},
		{"expired after NotAfter", base, notAfter.Add(time.Second), false, checker.ReasonExpired},

		{"revoked strictly after RevokedAt", revoked, revokedAt.Add(time.Hour), false, checker.ReasonRevoked},
		{"revoked boundary checkTime == RevokedAt (>=)", revoked, revokedAt, false, checker.ReasonRevoked},
		{"valid before RevokedAt", revoked, revokedAt.Add(-time.Hour), true, ""},
		{"expired beats revoked (after NotAfter)", revoked, notAfter.Add(time.Hour), false, checker.ReasonExpired},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			valid, reason := checker.Check(tc.cert, tc.at)
			if valid != tc.wantValid || reason != tc.wantReason {
				t.Errorf("Check() = (%v, %q), want (%v, %q)", valid, reason, tc.wantValid, tc.wantReason)
			}
		})
	}
}

func TestCheck_RevokedAtAfterNotAfter(t *testing.T) {
	// Cert was revoked AFTER it had already expired — for any time past NotAfter we still want "expired".
	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := notBefore.AddDate(1, 0, 0)
	revokedAt := notAfter.AddDate(0, 1, 0)

	cert := model.Certificate{
		Serial:    "FFFF",
		NotBefore: notBefore,
		NotAfter:  notAfter,
		RevokedAt: &revokedAt,
	}

	valid, reason := checker.Check(cert, notAfter.AddDate(0, 0, 7))
	if valid || reason != checker.ReasonExpired {
		t.Errorf("got (%v, %q), want (false, %q)", valid, reason, checker.ReasonExpired)
	}
}
