package storage_test

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
	"github.com/dukaev/cert-check-service/internal/storage"
)

// TestMemoryStore_GetReturnsDefensiveCopy — callers must not be able to corrupt
// the store via aliased pointers (RevokedAt, Serial slice).
//
// This defends against a class of bugs that wouldn't exist with Postgres but
// is subtle in MemoryStore; without the defensive copy the contract diverges
// between backends.
func TestMemoryStore_GetReturnsDefensiveCopy(t *testing.T) {
	s := storage.NewMemoryStore()
	serial, _ := hex.DecodeString("01A2B3")
	revoked := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	s.Put(model.Certificate{
		Serial:    serial,
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
		RevokedAt: &revoked,
	})

	// First read — mutate everything we can reach through the returned value.
	got1, err := s.Get(context.Background(), 0, serial)
	if err != nil {
		t.Fatal(err)
	}
	if got1.RevokedAt == nil {
		t.Fatal("RevokedAt unexpectedly nil")
	}
	*got1.RevokedAt = time.Time{}
	got1.Serial[0] = 0xFF

	// Second read — original data should be untouched.
	got2, err := s.Get(context.Background(), 0, serial)
	if err != nil {
		t.Fatal(err)
	}
	if got2.RevokedAt == nil || !got2.RevokedAt.Equal(revoked) {
		t.Errorf("store leaked RevokedAt: now %v, want %v", got2.RevokedAt, revoked)
	}
	if got2.Serial[0] != 0x01 {
		t.Errorf("store leaked Serial: byte0=%#x, want 0x01", got2.Serial[0])
	}
}
