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
//
// TODO(part-1): implement.
func Check(cert model.Certificate, checkTime time.Time) (valid bool, reason string) {
	return false, "TODO"
}
