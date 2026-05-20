package storage_test

import (
	"testing"

	"github.com/dukaev/cert-check-service/internal/model"
	"github.com/dukaev/cert-check-service/internal/storage"
	"github.com/dukaev/cert-check-service/internal/storage/storagetest"
)

// TestMemoryStore_Contract proves MemoryStore satisfies the Store contract.
// The same suite will be run against the Postgres implementation in Phase 2.
func TestMemoryStore_Contract(t *testing.T) {
	storagetest.RunStoreContract(t, func(seed []model.Certificate) storage.Store {
		s := storage.NewMemoryStore()
		for _, c := range seed {
			s.Put(c)
		}
		return s
	})
}
