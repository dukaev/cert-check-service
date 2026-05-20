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

## Measured results

Apple M-series, Go 1.25.6, `GOMAXPROCS=12`, 2 s warmup before vegeta.

### Native `go run` (production-relevant number)

```
target:     10 000 req/s, 30s, 4 targets (hot/revoked/expired/not_found)
throughput: 9993.6 req/s sustained
latency:    p50=85µs  p95=2.2ms  p99=19.5ms  max=93ms
success:    100%
allocs:     0 on hot path (checker.Check, MemoryStore.Get)
```

This is the number that anchors Parts 2 and 3 — `make run` запускает тот же бинарь, что и Docker, без сетевой прослойки.

### Docker container (`make docker && docker run -p 8080:8080`)

**Functional smoke — ✅ все эндпоинты корректны:**
- `/healthz` → 200 `ok`
- `/readyz` → 200 `ready` (MemoryStore.Ping)
- `/api/v1/check?serial=01A2B3` → `valid`
- `?serial=DEADBEEF` → `revoked`
- `?serial=E0E0E0E0` → `expired`
- `?serial=ABCD` → `not_found`
- `?serial=01A2B3&ca_id=42` → `not_found` (CA isolation works)
- `?serial=NOTHEX` → 400 JSON error

**Load test через Docker-on-macOS — с оговорками:**
```
target:     10 000 req/s, 30s
throughput: ~4 500 req/s effective
latency:    p50=386µs, p99=8.5s, max=30s
success:    98.14% (1.86% — port exhaustion / EOF)
```

⚠️ **Это не репрезентативная цифра для production.** Docker Desktop / OrbStack на macOS пускает TCP через VPN-туннель и NAT — при 10k connections/sec macOS-kernel упирается в port exhaustion (`bind: can't assign requested address`). Это **сетевая прослойка**, а не приложение.

**Чтобы получить честные production-цифры из контейнера:**
- Запустить на Linux-нативном Docker (без VM-туннеля)
- Или в k8s pod
- Или `vegeta` из соседнего контейнера в той же docker-сети
- На macOS — снижать `-rate` до ~3000 для получения < 1% ошибок (это **верхняя граница macOS-стека**, не сервиса)

Микробенчмарки (`make bench`) не зависят от сетевого стека и дают корректную картину независимо от платформы:

| Бенч | ns/op | allocs/op |
|---|---|---|
| `checker.Check` | 6.9 | 0 |
| `MemoryStore.Get` (RunParallel) | 173 | 0 |
| `Handler.Check` (RunParallel) | 1183 | 19 |
