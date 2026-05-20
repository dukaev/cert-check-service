package storage

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
)

// memKey composites (caID, serial) into the in-memory map key.
// Serial is upper-cased for case-insensitive lookup.
type memKey struct {
	caID   uint16
	serial string
}

// MemoryStore is an in-memory Store backed by a map + RWMutex.
// Production replacements (Postgres) implement the same Store interface.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[memKey]model.Certificate
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[memKey]model.Certificate)}
}

// Put inserts/updates a certificate. Idempotent on (CaID, Serial).
func (s *MemoryStore) Put(c model.Certificate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[memKey{caID: c.CaID, serial: strings.ToUpper(c.Serial)}] = c
}

// Get returns the certificate or ErrNotFound. ctx is honored for cancellation
// to keep the contract identical to a Postgres-backed implementation.
func (s *MemoryStore) Get(ctx context.Context, caID uint16, serial string) (model.Certificate, error) {
	if err := ctx.Err(); err != nil {
		return model.Certificate{}, err
	}
	s.mu.RLock()
	c, ok := s.data[memKey{caID: caID, serial: strings.ToUpper(serial)}]
	s.mu.RUnlock()
	if !ok {
		return model.Certificate{}, ErrNotFound
	}
	return c, nil
}

// Seed populates hard-coded certificates for local development.
func (s *MemoryStore) Seed() {
	now := time.Now().UTC()
	revoked := now.Add(-24 * time.Hour)
	s.Put(model.Certificate{
		Serial:    "01A2B3",
		NotBefore: now.AddDate(-1, 0, 0),
		NotAfter:  now.AddDate(1, 0, 0),
	})
	s.Put(model.Certificate{
		Serial:    "DEADBEEF",
		NotBefore: now.AddDate(-1, 0, 0),
		NotAfter:  now.AddDate(1, 0, 0),
		RevokedAt: &revoked,
	})
	s.Put(model.Certificate{
		Serial:    "E0E0E0E0", // expired (hex-valid, 8 chars)
		NotBefore: now.AddDate(-2, 0, 0),
		NotAfter:  now.AddDate(-1, 0, 0),
	})
}
