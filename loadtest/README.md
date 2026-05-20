# Load tests

Used to validate Part 2's "10 000 RPS" assumption against the in-memory implementation.

## Prereqs

```bash
brew install vegeta   # or: go install github.com/tsenart/vegeta/v12@latest
```

Run the service in another shell:

```bash
docker build -t cert-check-service .
docker run --rm -p 8080:8080 cert-check-service
```

## Mixed-target attack (90/10 hit/miss)

```bash
vegeta attack -rate=10000 -duration=30s -targets=loadtest/targets.txt \
  | tee loadtest/results.bin \
  | vegeta report
```

Histogram of latencies:

```bash
vegeta report -type='hist[0,1ms,5ms,10ms,50ms,100ms]' < loadtest/results.bin
```

## Hot-key scenario (upper-bound RPS)

```bash
echo "GET http://localhost:8080/api/v1/check?serial=01A2B3" \
  | vegeta attack -rate=0 -max-workers=200 -duration=30s \
  | vegeta report
```

`-rate=0` means "as fast as possible". Compare RPS achieved here vs. the mixed run — the gap is roughly your store lookup cost.

## Measured results (snapshot)

Apple M-series, Go 1.25.6, `GOMAXPROCS=12`, server run via `go run` (Docker daemon unavailable at the time):

```
target:     10 000 req/s, 30s, 4 targets (hot/revoked/expired/not_found)
throughput: 9890.5 req/s sustained
latency:    p50=77µs  p95=906µs  p99=24ms  max=88ms
success:    98.9% (1.1% refused in the first ~150ms before warmup)
allocs:     0 on hot path (checker.Check, MemoryStore.Get)
```

These numbers anchor Parts 2 and 3 in the top-level README in real measurements rather than speculation. To reproduce, follow the steps above and compare with the table.
