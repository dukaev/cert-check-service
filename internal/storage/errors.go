package storage

import "errors"

// ErrNotFound — certificate with the requested (caID, serial) does not exist.
// Handlers translate this into reason="not_found", HTTP 200.
var ErrNotFound = errors.New("storage: certificate not found")

// ErrUnavailable — backing store is temporarily unreachable.
// Reserved for the Postgres implementation; handlers should map to 503.
var ErrUnavailable = errors.New("storage: backend unavailable")
