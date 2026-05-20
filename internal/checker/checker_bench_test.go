package checker_test

import (
	"testing"
	"time"

	"github.com/dukaev/cert-check-service/internal/checker"
	"github.com/dukaev/cert-check-service/internal/model"
)

// Run: go test -bench=. -benchmem -benchtime=5s -count=5 ./internal/checker/...
func BenchmarkCheck(b *testing.B) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cert := model.Certificate{
		Serial:    "01A2B3",
		NotBefore: now.Add(-30 * 24 * time.Hour),
		NotAfter:  now.Add(30 * 24 * time.Hour),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = checker.Check(cert, now)
	}
}
