package storage_test

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/dukaev/cert-check-service/internal/storage"
)

// TestMemoryStore_Seed verifies Seed populates the documented fixtures and
// each fixture is retrievable through the public Get contract.
func TestMemoryStore_Seed(t *testing.T) {
	s := storage.NewMemoryStore()
	s.Seed()

	want := []string{"01A2B3", "DEADBEEF", "E0E0E0E0"}
	for _, hexSerial := range want {
		serial, err := hex.DecodeString(hexSerial)
		if err != nil {
			t.Fatalf("bad fixture %q: %v", hexSerial, err)
		}
		c, err := s.Get(context.Background(), 0, serial)
		if err != nil {
			t.Errorf("Seed didn't populate %s: %v", hexSerial, err)
			continue
		}
		if len(c.Serial) == 0 {
			t.Errorf("Seed populated %s but Serial is empty", hexSerial)
		}
	}
}

// TestMemoryStore_SeedDoesNotPopulateRandomSerials negative case — sanity.
func TestMemoryStore_SeedDoesNotPopulateRandomSerials(t *testing.T) {
	s := storage.NewMemoryStore()
	s.Seed()

	unexpected, _ := hex.DecodeString("CAFEBABE")
	_, err := s.Get(context.Background(), 0, unexpected)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get(CAFEBABE) err = %v, want ErrNotFound", err)
	}
}
