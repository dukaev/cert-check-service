package storage

import (
	"sync"
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
)

// MemoryStore is an in-memory Store backed by a map + RWMutex.
// Production replacements (Postgres, Redis) should implement the same Store interface.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]model.Certificate
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]model.Certificate)}
}

// Put inserts/updates a certificate. Serial is stored as-is — callers should normalize case.
func (s *MemoryStore) Put(c model.Certificate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[c.Serial] = c
}

// Get is the hot path — should be zero-allocation.
//
// TODO(part-1): implement.
func (s *MemoryStore) Get(serial string) (model.Certificate, bool) {
	return model.Certificate{}, false
}

// Seed populates a few hard-coded certificates for local development.
// TODO(part-1): add realistic fixtures (valid, expired, revoked, future).
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
		Serial:    "EXPIRED1",
		NotBefore: now.AddDate(-2, 0, 0),
		NotAfter:  now.AddDate(-1, 0, 0),
	})
}
