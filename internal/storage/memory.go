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

// Get is the hot path — must be zero-allocation.
//
// TODO(part-1): implement.
func (s *MemoryStore) Get(ctx context.Context, caID uint16, serial string) (model.Certificate, error) {
	_ = ctx // unused until real I/O is added
	_ = caID
	_ = serial
	return model.Certificate{}, ErrNotFound
}

// Seed populates hard-coded certificates for local development.
// TODO(part-1): expand fixtures (valid, expired, revoked, future).
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
