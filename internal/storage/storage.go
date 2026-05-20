package storage

import (
	"context"

	"github.com/dukaev/cert-check-service/internal/model"
)

// Store is the read-side abstraction over the certificate repository.
//
// Production implementations (Postgres, Postgres+cache) MUST satisfy this
// same contract — see storagetest.RunStoreContract.
//
// Signature rationale (per ARCHITECTURE.md §"Архитектурные швы"):
//   - context.Context — timeouts, tracing, cancellation; Postgres can't live without it.
//   - caID — serial is unique only within a CA.
//   - typed errors — handlers should switch on ErrNotFound, not on a bool.
type Store interface {
	Get(ctx context.Context, caID uint16, serial string) (model.Certificate, error)
}
