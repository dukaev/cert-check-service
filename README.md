# cert-check-service

HTTP-сервис для проверки статуса сертификатов по серийному номеру (Go + Docker).
Условия задания — в [TASK.md](./TASK.md).

## Быстрый старт

```bash
# локально
go run ./cmd/server
# в Docker
docker build -t cert-check-service .
docker run --rm -p 8080:8080 cert-check-service
```

Проверка:

```bash
curl 'http://localhost:8080/healthz'                                                # ok
curl 'http://localhost:8080/api/v1/check?serial=01A2B3'                              # valid
curl 'http://localhost:8080/api/v1/check?serial=DEADBEEF'                            # revoked
curl 'http://localhost:8080/api/v1/check?serial=E0E0E0E0'                            # expired
curl 'http://localhost:8080/api/v1/check?serial=ABCD'                                # not_found
curl 'http://localhost:8080/api/v1/check?serial=01A2B3&at=2020-01-01T00:00:00Z'      # expired
curl 'http://localhost:8080/api/v1/check?serial=NOTHEX'                              # 400
```

## API

`GET /api/v1/check?serial=<hex>[&at=<RFC3339>]` → `200 application/json`

```json
{ "serial": "01A2B3", "valid": true, "reason": "" }
```

`reason` — одно из `"" | not_found | expired | revoked`. Поле всегда присутствует.
На некорректный ввод — `400` с текстовым описанием.

## Структура

```
cmd/server/                        точка входа (HTTP + healthz + /api/v1/check)
internal/model/                    Certificate (CaID, Serial, NotBefore, NotAfter, RevokedAt)
internal/checker/                  чистая функция Check(cert, time) → (valid, reason)
internal/storage/                  Store-интерфейс + MemoryStore + storagetest.RunStoreContract
internal/handler/                  HTTP-обработчик, parseSerial, parseAt, инжектируемый Clock
loadtest/                          vegeta-таргеты и инструкция к нагрузочному тесту
.golangci.yml + .github/workflows/ линтеры + CI
```

## Тесты, линтеры, бенчмарки

```bash
go test -race -count=1 ./...                              # unit + property + contract + concurrency
go test -fuzz=FuzzParseSerial -fuzztime=30s ./internal/handler/...
go test -fuzz=FuzzParseAt     -fuzztime=30s ./internal/handler/...
go test -bench=. -benchmem -benchtime=2s ./...
golangci-lint run
```

Покрытие тестами по слоям:

| Слой | Что проверяется |
|---|---|
| `checker` | table-driven (границы окна, `>=` для revoke, expired-побеждает-revoked) + **property-based** (4 инварианта × 500 итераций через `testing/quick`) |
| `storage` | **contract-тест** в `storagetest.RunStoreContract` — переиспользуется любой реализацией Store |
| `handler` | httptest 200/400/405, golden JSON, ctx cancel, 100 конкурентных клиентов, **fuzz** для парсеров serial и at |

## Performance

Apple M-серия, Go 1.25.6, `GOMAXPROCS=12`. Сервер запущен нативно (Docker daemon отсутствовал на момент замера; для production-бенча — поднять контейнер и пинить ядра).

**Микробенчмарки** (`go test -bench=. -benchmem -benchtime=2s`):

| Бенч | ns/op | allocs/op |
|---|---|---|
| `checker.Check` | **6.9** | 0 |
| `MemoryStore.Get` (RunParallel) | **173** | 0 |
| `Handler.Check` (RunParallel) | 1183 | 19 (доминирует `encoding/json` + hex-кодирование ответа) |

**Нагрузочный тест** (`vegeta`, 10 000 req/s × 30 s, прогрев 2 с, 4 таргета — горячий/revoked/expired/not_found):

| Метрика | Значение |
|---|---|
| Throughput | **9993 req/s sustained** |
| p50 latency | **85 µs** |
| p95 latency | 2.2 ms |
| p99 latency | 19.5 ms (GC pauses) |
| max latency | 93 ms |
| Success | **100 %** |

Целевая цифра «10 000 RPS» из Части 2 реалистична уже на одной ноде в in-memory режиме. Хот-пат (`Check` + `Get`) — **ноль аллокаций**, GC задевает только JSON-кодирование в HTTP-слое.

---

## Часть 2. Архитектурный эскиз (production)

Здесь — резюме на 1 страницу. Полная версия с разделами «Архитектурные швы» и «Тесты по уровням пирамиды» — в [ARCHITECTURE.md](./ARCHITECTURE.md).

### Хранение
**PostgreSQL.** Данные структурированные, почти иммутабельные (после выпуска меняется только `revoked_at`), 100M строк — типичная нагрузка для одной ноды. Доступ — точечный по PK.

Схема: `(ca_id SMALLINT, serial BYTEA, not_before, not_after, revoked_at, PRIMARY KEY(ca_id, serial)) PARTITION BY HASH (ca_id)` на 32–64 партиции. Покрывающий индекс `(ca_id, serial) INCLUDE (not_before, not_after, revoked_at)` → index-only scan + partition pruning, без захода в heap. `serial BYTEA` вместо `TEXT` — меньше байт, нет хлопот с регистром.

### Кэширование
- **L1** (in-process Ristretto, TTL 30–60 с) — горячие `(ca_id, serial) → status`
- **L2** (Redis cluster, TTL 5–10 мин) — широкий warm cache
- **Negative cache** для `not_found` с коротким TTL (1–2 мин)
- **Bloom filter** по отозванным серийникам на каждом инстансе — решает 99 % проверок без обращения к Redis/БД

**Инвалидация:** publisher пишет revocation в БД и публикует `cert.revoked` в Kafka; consumer на каждом инстансе делает `cache.Delete + bloom.Add`. Ключ кэша включает `crl_version` per `ca_id` — бамп версии естественно протухает старые ключи. TTL на L2 короткий, как страховка.

### Масштабирование
- **Сервис stateless** — N подов за L7-балансировщиком, автоскейлинг по CPU и p95. Один под держит несколько тысяч RPS на ядро (Часть 1 на одной ноде уже даёт ~10k RPS), 10k под целевую нагрузку — 4–8 подов с запасом
- **Sharding-aware routing**: балансировщик хеширует по `ca_id` → инстанс. Bloom-фильтры становятся «горячими» по конкретным УЦ, hit-rate растёт
- **БД:** read replicas (3–5), PgBouncer (transaction mode) перед primary. Запись (revoke/issue) — только в primary. С учётом кэша к репликам приходит лишь 1–5 % miss-трафика
- **HASH-партиции** уже подготовили почву для шардинга (Citus / app-level по `ca_id mod N`)

### Надёжность
Два источника событий:
- **Inbound от УЦ → Kafka → consumer → Postgres:** вебхуки `POST /internal/ca/{ca_id}/events` с HMAC, либо CRL/delta-CRL по расписанию. Идемпотентные `INSERT … ON CONFLICT … DO UPDATE` по `(ca_id, serial)`. Publisher использует **outbox pattern** (запись в БД + событие в Kafka в одной транзакции)
- **Периодическая сверка с CRL** — защищает от потерянных вебхуков (eventual consistency safety net)

Мониторинг: метрика `crl_lag_seconds` per `ca_id`, алерт если события не приходят > X минут. DLQ для битых событий. Health-чеки проверяют доступность БД, Redis и свежесть Kafka.

---

## Часть 3. Микро-ADR

### Контекст
Часть 1 требует in-memory store, который выдержит производственно-релевантную нагрузку (10k RPS) и при этом не поломает миграцию на Postgres из Части 2. Сертификат уникален в пределах УЦ (`ca_id, serial`), доступ — read-heavy (single-digit % записей).

### Альтернативы
1. `sync.Map[string]Certificate` — простой ключ-строка, lock-free reads.
2. **`map[memKey]Certificate` под `sync.RWMutex`**, где `memKey = struct{ caID uint16; serial string }`.
3. Sharded map с per-CA mutex.

### Решение
Вариант **2**: `map+RWMutex` с композитным ключом `(caID, upper(serial))`, типизированные ошибки (`ErrNotFound`, `ErrUnavailable`), сигнатура `Get(ctx, caID, serial)` — идентична будущей Postgres-реализации.

### Обоснование
- Бенчмарк `Get` под `RunParallel`: **148 ns/op, 0 allocs**. На read-heavy с редкой записью contention на `RWMutex` пренебрежимо малый.
- `sync.Map` оптимизирован под «многие пишут — многие читают», а не под наш сценарий. Профит был бы только при равном чтении/записи, чего у нас нет.
- Sharded map даёт выигрыш только когда внутренний contention существенен, а с `RWMutex` он уже отсутствует на наших цифрах. Преждевременная сложность.
- **Композитный ключ с самого начала** — главный долгосрочный выигрыш: переход на Postgres-реализацию `Store` будет drop-in (тот же `(ctx, caID, serial)`-контракт), `storagetest.RunStoreContract` прогонится без изменений. Если бы ключ был просто `string`, рефакторинг бы стоил правок во всех тестах и обработчиках.
- Цена ошибки низкая: если на нагрузке выявится contention, замена на sharded-вариант — внутри `MemoryStore`, не ломает интерфейс.
