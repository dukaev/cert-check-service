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

## What to record in the project README

After running, fill in the table in the top-level `README.md` under **Performance**:

```
hardware:   <CPU model, cores>
target:     10 000 req/s, 30s
results:    XX.X k req/s, p50=X.XXms, p95=X.XXms, p99=X.XXms, errors=0
allocs:     0 on hot path (from `go test -bench`)
```

These numbers anchor the architectural sketch (Part 2) and the ADR (Part 3) in real measurements rather than speculation.
