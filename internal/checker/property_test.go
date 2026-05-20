package checker_test

import (
	"testing"
	"testing/quick"
	"time"

	"github.com/dukaev/cert-check-service/internal/checker"
	"github.com/dukaev/cert-check-service/internal/model"
)

// Property-based tests per ARCHITECTURE.md §"Тесты — по уровням пирамиды" / 2.
// Each property is a statement that MUST hold for any input — quick.Check
// generates random inputs and fails on counter-examples.

const propIterations = 500

var propConfig = &quick.Config{MaxCount: propIterations}

var base = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// Property 1: at ∈ [NotBefore, NotAfter] && RevokedAt == nil → always valid.
func TestProperty_ValidWhenInWindowAndNotRevoked(t *testing.T) {
	t.Parallel()
	f := func(notBeforeOffset uint32, windowSize uint32, atOffset uint32) bool {
		windowSize = windowSize%(10*365*24*3600) + 1 // (0, ~10y]
		atOffset = atOffset % windowSize             // at ∈ [NotBefore, NotAfter)

		notBefore := base.Add(time.Duration(notBeforeOffset) * time.Second)
		notAfter := notBefore.Add(time.Duration(windowSize) * time.Second)
		at := notBefore.Add(time.Duration(atOffset) * time.Second)

		cert := model.Certificate{NotBefore: notBefore, NotAfter: notAfter}
		valid, reason := checker.Check(cert, at)
		return valid && reason == ""
	}
	if err := quick.Check(f, propConfig); err != nil {
		t.Error(err)
	}
}

// Property 2: at > NotAfter → always expired (regardless of RevokedAt).
func TestProperty_ExpiredAfterNotAfter(t *testing.T) {
	t.Parallel()
	f := func(windowSize uint32, pastNotAfter uint32, revokeOffset uint32) bool {
		windowSize = windowSize%(365*24*3600) + 1
		pastNotAfter = pastNotAfter%(365*24*3600) + 1 // strictly after NotAfter

		notBefore := base
		notAfter := notBefore.Add(time.Duration(windowSize) * time.Second)
		at := notAfter.Add(time.Duration(pastNotAfter) * time.Second)

		var rev *time.Time
		if revokeOffset%2 == 0 {
			r := notBefore.Add(time.Duration(revokeOffset) * time.Second)
			rev = &r
		}
		cert := model.Certificate{NotBefore: notBefore, NotAfter: notAfter, RevokedAt: rev}
		_, reason := checker.Check(cert, at)
		return reason == checker.ReasonExpired
	}
	if err := quick.Check(f, propConfig); err != nil {
		t.Error(err)
	}
}

// Property 3: if Check(c, at) is valid, then setting RevokedAt=at flips result to revoked.
func TestProperty_RevokingAtCheckTimeFlipsToRevoked(t *testing.T) {
	t.Parallel()
	f := func(notBeforeOffset uint32, windowSize uint32, atOffset uint32) bool {
		windowSize = windowSize%(365*24*3600) + 1
		atOffset = atOffset % windowSize

		notBefore := base.Add(time.Duration(notBeforeOffset) * time.Second)
		notAfter := notBefore.Add(time.Duration(windowSize) * time.Second)
		at := notBefore.Add(time.Duration(atOffset) * time.Second)

		cert := model.Certificate{NotBefore: notBefore, NotAfter: notAfter}
		valid, _ := checker.Check(cert, at)
		if !valid {
			return true // precondition not met, trivially holds
		}
		cert.RevokedAt = &at
		_, reason := checker.Check(cert, at)
		return reason == checker.ReasonRevoked
	}
	if err := quick.Check(f, propConfig); err != nil {
		t.Error(err)
	}
}

// Property 4: determinism — same input twice yields same output.
func TestProperty_Deterministic(t *testing.T) {
	t.Parallel()
	f := func(nb, na, at int64, revKind uint8) bool {
		notBefore := time.Unix(nb%int64(50*365*24*3600), 0)
		notAfter := time.Unix(na%int64(50*365*24*3600), 0)
		checkAt := time.Unix(at%int64(50*365*24*3600), 0)

		var rev *time.Time
		if revKind%2 == 0 {
			r := time.Unix(int64(revKind)*1000, 0)
			rev = &r
		}
		cert := model.Certificate{NotBefore: notBefore, NotAfter: notAfter, RevokedAt: rev}

		v1, r1 := checker.Check(cert, checkAt)
		v2, r2 := checker.Check(cert, checkAt)
		return v1 == v2 && r1 == r2
	}
	if err := quick.Check(f, propConfig); err != nil {
		t.Error(err)
	}
}
