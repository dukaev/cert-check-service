package model

import "time"

// Certificate is the domain object. Serial is unique only within a CA, so
// every operation that locates a certificate must be parameterised by CaID.
type Certificate struct {
	CaID      uint16
	Serial    string
	NotBefore time.Time
	NotAfter  time.Time
	RevokedAt *time.Time
}
