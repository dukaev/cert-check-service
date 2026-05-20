package storage_test

import (
	"sync"
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
	"github.com/dukaev/cert-check-service/internal/storage"
)

func TestMemoryStore_GetExisting(t *testing.T) {
	s := storage.NewMemoryStore()
	cert := model.Certificate{
		Serial:    "01A2B3",
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
	}
	s.Put(cert)

	got, ok := s.Get("01A2B3")
	if !ok {
		t.Fatal("Get returned ok=false for existing serial")
	}
	if got.Serial != cert.Serial {
		t.Errorf("got serial %q, want %q", got.Serial, cert.Serial)
	}
}

func TestMemoryStore_GetMissing(t *testing.T) {
	s := storage.NewMemoryStore()
	got, ok := s.Get("NOPE")
	if ok {
		t.Fatalf("Get returned ok=true for missing serial, got %+v", got)
	}
}

// TestMemoryStore_ConcurrentReadWrite — passes only under `go test -race`.
func TestMemoryStore_ConcurrentReadWrite(t *testing.T) {
	s := storage.NewMemoryStore()
	s.Put(model.Certificate{Serial: "AA", NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)})

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func() { defer wg.Done(); _, _ = s.Get("AA") }()
		go func() {
			defer wg.Done()
			s.Put(model.Certificate{Serial: "BB", NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)})
		}()
	}
	wg.Wait()
}
