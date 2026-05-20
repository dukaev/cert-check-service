package model

import "time"

// Certificate is the domain object.
//
// Serial is the raw binary value (Postgres BYTEA), not a hex string —
// avoids a hex↔binary round-trip on both sides of the storage layer.
// API I/O hex-encodes at the boundary (handler), internal code passes []byte.
//
// Serial is unique only within a CA, so every operation that locates a
// certificate must be parameterised by CaID.
type Certificate struct {
	CaID      uint16
	Serial    []byte
	NotBefore time.Time
	NotAfter  time.Time
	RevokedAt *time.Time
}
