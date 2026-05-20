// Package storagetest provides a reusable contract test that every Store
// implementation MUST pass — in-memory today, Postgres tomorrow.
//
// Usage (from any package that implements storage.Store):
//
//	func TestMyStore_Contract(t *testing.T) {
//	    storagetest.RunStoreContract(t, func(seed []model.Certificate) storage.Store {
//	        s := newMyStore()
//	        for _, c := range seed { s.Put(c) }
//	        return s
//	    })
//	}
package storagetest

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/model"
	"github.com/dukaev/cert-check-service/internal/storage"
)

// Factory builds a Store pre-populated with the given fixtures.
// Tests pass deterministic data so the contract is reproducible across backends.
type Factory func(seed []model.Certificate) storage.Store

// RunStoreContract exercises the Store interface against a backend.
// Every Store implementation MUST pass this suite without modification.
func RunStoreContract(t *testing.T, newStore Factory) {
	t.Helper()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	serial := mustHex(t, "01A2B3")
	cert := model.Certificate{
		CaID:      0,
		Serial:    serial,
		NotBefore: base,
		NotAfter:  base.AddDate(1, 0, 0),
	}

	t.Run("Get_existing", func(t *testing.T) {
		s := newStore([]model.Certificate{cert})
		got, err := s.Get(context.Background(), 0, serial)
		if err != nil {
			t.Fatalf("Get err = %v, want nil", err)
		}
		if !bytes.Equal(got.Serial, cert.Serial) {
			t.Errorf("Get serial = %x, want %x", got.Serial, cert.Serial)
		}
	})

	t.Run("Get_missing_returns_ErrNotFound", func(t *testing.T) {
		s := newStore(nil)
		_, err := s.Get(context.Background(), 0, mustHex(t, "BADC0FFE"))
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Get err = %v, want ErrNotFound", err)
		}
	})

	t.Run("Get_wrong_ca_id_returns_ErrNotFound", func(t *testing.T) {
		// Serial is unique only within a CA. Same serial under different CA must miss.
		s := newStore([]model.Certificate{cert})
		_, err := s.Get(context.Background(), 1, serial)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Get err = %v, want ErrNotFound", err)
		}
	})

	t.Run("Get_byte_identity", func(t *testing.T) {
		// Two independently decoded byte slices for the same hex must hit the same row.
		s := newStore([]model.Certificate{cert})
		duplicate := mustHex(t, "01A2B3") // separately allocated
		_, err := s.Get(context.Background(), 0, duplicate)
		if err != nil {
			t.Errorf("Get err = %v, want nil (lookup must be content-equal, not pointer-equal)", err)
		}
	})

	t.Run("Get_respects_context_cancellation", func(t *testing.T) {
		s := newStore([]model.Certificate{cert})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// We do not assert the specific error — in-memory may ignore ctx, Postgres won't.
		// We only assert the call returns at all (no hang).
		done := make(chan struct{})
		go func() {
			_, _ = s.Get(ctx, 0, serial)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Get hung on canceled context")
		}
	})

	t.Run("Concurrent_reads_safe", func(t *testing.T) {
		s := newStore([]model.Certificate{cert})
		var wg sync.WaitGroup
		const n = 100
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				_, _ = s.Get(context.Background(), 0, serial)
			}()
		}
		wg.Wait()
	})
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture %q: %v", s, err)
	}
	return b
}
