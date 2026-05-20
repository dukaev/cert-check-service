package storage_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
	"github.com/dukaev/cert-check-service/internal/storage"
)

// Run: go test -bench=. -benchmem ./internal/storage/...
// Compare with a sync.Map-backed variant to validate the map+RWMutex choice for the ADR.
func BenchmarkMemoryStore_Get(b *testing.B) {
	s := storage.NewMemoryStore()
	for i := 0; i < 100_000; i++ {
		s.Put(model.Certificate{
			Serial:    strconv.Itoa(i),
			NotBefore: time.Now(),
			NotAfter:  time.Now().Add(time.Hour),
		})
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = s.Get(ctx, 0, strconv.Itoa(i%100_000))
			i++
		}
	})
}
