package checker

import (
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
)

const (
	ReasonNotFound = "not_found"
	ReasonExpired  = "expired"
	ReasonRevoked  = "revoked"
)

// Check returns (valid, reason) for cert at checkTime per the spec rules:
//  1. checkTime ∈ [NotBefore, NotAfter] (inclusive) — otherwise "expired".
//  2. If RevokedAt != nil && checkTime >= *RevokedAt — "revoked".
//  3. Otherwise valid, reason == "".
//
// Order matters: expired wins over revoked (window is checked first).
func Check(cert model.Certificate, checkTime time.Time) (valid bool, reason string) {
	if checkTime.Before(cert.NotBefore) || checkTime.After(cert.NotAfter) {
		return false, ReasonExpired
	}
	if cert.RevokedAt != nil && !checkTime.Before(*cert.RevokedAt) {
		return false, ReasonRevoked
	}
	return true, ""
}
