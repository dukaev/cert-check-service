package storage_test

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
	"github.com/dukaev/cert-check-service/internal/storage"
)

// Run: go test -bench=. -benchmem ./internal/storage/...
// Compare with a sync.Map-backed variant to validate the map+RWMutex choice for the ADR.
func BenchmarkMemoryStore_Get(b *testing.B) {
	s := storage.NewMemoryStore()
	serials := make([][]byte, 100_000)
	for i := range serials {
		serial := make([]byte, 4)
		binary.BigEndian.PutUint32(serial, uint32(i))
		serials[i] = serial
		s.Put(model.Certificate{
			Serial:    serial,
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
			_, _ = s.Get(ctx, 0, serials[i%len(serials)])
			i++
		}
	})
}
