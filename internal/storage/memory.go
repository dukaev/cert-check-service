package storage

import (
	"context"
	"encoding/hex"
	"sync"
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
)

// memKey composites (caID, serial) into the in-memory map key.
// Go map keys can't be []byte, so we coerce via string(bytes) — Go optimizes
// this conversion to be allocation-free for the lookup path.
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
// The certificate is copied so subsequent caller mutations don't corrupt the store.
func (s *MemoryStore) Put(c model.Certificate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[memKey{caID: c.CaID, serial: string(c.Serial)}] = cloneCert(c)
}

// Get returns the certificate or ErrNotFound. ctx is honored for cancellation
// to keep the contract identical to a Postgres-backed implementation.
//
// The returned Certificate is a defensive copy — callers cannot mutate the
// underlying RevokedAt pointer and corrupt the store.
func (s *MemoryStore) Get(ctx context.Context, caID uint16, serial []byte) (model.Certificate, error) {
	if err := ctx.Err(); err != nil {
		return model.Certificate{}, err
	}
	s.mu.RLock()
	c, ok := s.data[memKey{caID: caID, serial: string(serial)}]
	s.mu.RUnlock()
	if !ok {
		return model.Certificate{}, ErrNotFound
	}
	return cloneCert(c), nil
}

// Ping satisfies storage.Readier — MemoryStore is always ready as long as
// the process is alive. The Postgres-backed implementation will use ctx
// to actually query SELECT 1.
func (s *MemoryStore) Ping(ctx context.Context) error {
	return ctx.Err()
}

// Seed populates hard-coded certificates for local development.
func (s *MemoryStore) Seed() {
	now := time.Now().UTC()
	revoked := now.Add(-24 * time.Hour)
	s.Put(model.Certificate{
		Serial:    mustHex("01A2B3"),
		NotBefore: now.AddDate(-1, 0, 0),
		NotAfter:  now.AddDate(1, 0, 0),
	})
	s.Put(model.Certificate{
		Serial:    mustHex("DEADBEEF"),
		NotBefore: now.AddDate(-1, 0, 0),
		NotAfter:  now.AddDate(1, 0, 0),
		RevokedAt: &revoked,
	})
	s.Put(model.Certificate{
		Serial:    mustHex("E0E0E0E0"),
		NotBefore: now.AddDate(-2, 0, 0),
		NotAfter:  now.AddDate(-1, 0, 0),
	})
}

// cloneCert deep-copies the Certificate so callers don't share the
// RevokedAt pointer with anything else (Store internals or other callers).
func cloneCert(c model.Certificate) model.Certificate {
	out := c
	if c.RevokedAt != nil {
		t := *c.RevokedAt
		out.RevokedAt = &t
	}
	if c.Serial != nil {
		out.Serial = append([]byte(nil), c.Serial...)
	}
	return out
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("storage.mustHex: " + err.Error())
	}
	return b
}
