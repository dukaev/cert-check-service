# Design Doc: cert-check-service — Production Architecture

**Status:** Draft
**Author:** dukaev999@gmail.com
**Last updated:** 2026-05-20
**Reviewers:** TBD

> Документ написан в формате **Google-style Engineering Design Doc** — индустриальный де-факто стандарт для production-предложений. Структура: tl;dr → Background → Goals/Non-Goals → Requirements → Detailed Design → ADRs → Operations → Risks → Rollout → Alternatives → Open Questions. Каждый компонент явно разделяет «реализовано в Phase 1» от «спроектировано для Phase 2».
>
> Связанные документы: [README.md](./README.md) (быстрый старт, API), [TASK.md](./TASK.md) (условия задания), [ARCHITECTURE.md](./ARCHITECTURE.md) (рабочие заметки про швы и пирамиду тестов).

---

## tl;dr

Сервис проверки статуса сертификатов: stateless Go-приложение за L7-балансировщиком, in-memory Phase 1 → Postgres + Redis + Kafka в Phase 2. Целевая нагрузка — 10 000 RPS, 100 млн записей, 100 УЦ. Phase 1 (in-memory) **реализован** и держит ~10k RPS на одной 12-ядерной ноде с p99 ≈ 20 ms. Phase 2 (production stack) — **спроектирован**, оценка реализации: 17–20 человеко-дней. Все архитектурные швы (`Store`, `Readier`, `Clock`, чистая `Check`, типизированные ошибки) подготовлены в Phase 1 так, что Phase 2 — это аддитивная работа без рефакторинга существующего кода.

---

## Background

Удостоверяющий центр (CA) выпускает сертификаты, которые потом нужно валидировать (для аудита, для proxy-сервисов, для compliance-проверок). Задача — внутренний сервис, отвечающий на вопрос «действителен ли сертификат с серийным номером S в момент времени T?».

**Текущий контекст:**
- 100 независимых УЦ
- 100 млн выпущенных сертификатов
- 10 000 RPS пиковая нагрузка
- доля отозванных — обычно < 1 %, но растёт со временем
- propagation revocation важна (стейлый «valid» ответ для скомпрометированного серта = security incident)

**Чего сейчас НЕТ в Phase 1:**
- персистентного хранилища (in-memory map)
- горизонтального масштабирования (один процесс)
- источника событий (issuance/revocation статически захардкожены)
- кэширования (всё лежит в RAM, сам себе кэш)

Этот документ описывает целевую production-архитектуру и путь к ней.

---

## Goals

| # | Goal | Метрика |
|---|---|---|
| G1 | Корректность — `valid/expired/revoked/not_found` ровно по правилам ТЗ | 100 % property-based + table-driven покрытие |
| G2 | Низкая латентность | p99 < 50 ms сквозная |
| G3 | Высокая пропускная способность | 10 000 RPS sustained на 4–8 подах |
| G4 | Высокая доступность | 99.9 % monthly (≤ 43.2 мин downtime/мес) |
| G5 | Быстрая propagation revocation | p99 < 30 с от revoke до видимости во всех инстансах |
| G6 | Горизонтально масштабируемое хранилище | до 1 млрд записей без архитектурного передела |
| G7 | Эволюционное развитие — переход in-memory → Postgres без переписывания вызывающего кода | drop-in замена реализации `Store` |

## Non-Goals

| # | Non-Goal | Почему вне scope |
|---|---|---|
| NG1 | OCSP responder | Другой протокол, отдельный customer-ask |
| NG2 | Выпуск сертификатов | Сервис **consumer** событий, не producer |
| NG3 | Multi-region active-active | Сложность не оправдана для v1; single-region при 99.9 % достаточно |
| NG4 | Аутентификация клиентов | Внутренний сервис за mTLS-gateway; ZeroTrust — отдельный layer |
| NG5 | Certificate Transparency log integration | Отдельная inicjativa |
| NG6 | Web UI / Dashboard | Только программный API |

---

## Glossary

| Термин | Значение |
|---|---|
| **CA** | Certificate Authority — удостоверяющий центр |
| **CRL** | Certificate Revocation List — список отозванных сертификатов |
| **delta-CRL** | инкрементальный CRL с момента последнего полного |
| **OCSP** | Online Certificate Status Protocol (не используем, см. NG1) |
| **Outbox pattern** | паттерн для гарантированной публикации событий из БД-транзакции |
| **propagation lag** | время от revocation в primary БД до видимости во всех L1-кэшах |
| **Phase 1** | текущая in-memory реализация (готова) |
| **Phase 2** | целевая production-архитектура (этот документ) |

---

## Requirements

### Functional

- `GET /api/v1/check?serial=<hex>[&ca_id=<u16>][&at=<RFC3339>]`
- Ответ: `{serial, valid, reason}` где `reason ∈ {"", "not_found", "expired", "revoked"}`
- Серийник уникален в пределах УЦ; запросы без `ca_id` интерпретируются как УЦ #0 (backward-compat)
- Время по умолчанию — серверное `now()`

### Non-Functional (SLO)

| SLI | SLO | Burn rate alert |
|---|---|---|
| Availability | 99.9 % monthly | > 14.4 (fast burn, 1-hour budget) |
| p99 sequential latency | ≤ 50 ms | > 75 ms over 5 min |
| Error rate (5xx) | ≤ 0.01 % | > 0.1 % over 5 min |
| Revocation propagation p99 | ≤ 30 с | > 1 min over 10 min |
| Cache hit rate L1 | ≥ 95 % | < 80 % over 15 min |
| Kafka consumer lag | ≤ 10 с | > 60 с |

### Capacity planning

- **Storage size:** 100M строк × ~100 B/строка ≈ 10 ГБ. Покрывающий индекс — ещё ~6 ГБ. Полностью влезает в RAM одной machine.
- **Write rate:** при равномерной выдаче 100M certs / 10 лет = ~30 inserts/сек, мизер. Burst — учитываем (см. §Risks/CA-compromise).
- **Read rate:** 10 000 RPS sustained.
- **Cache memory:** L1 (Ristretto) 100 МБ × 8 подов = 800 МБ. L2 Redis cluster — 10 ГБ под весь датасет.
- **Bloom filter:** 1.2 МБ на 1M отозванных при FP rate 1 %, × 8 подов = ~10 МБ.

---

## High-Level Architecture

```
                       ┌───────────────────┐
                       │      Client       │
                       └─────────┬─────────┘
                                 │ HTTPS
                                 │ ?ca_id=&serial=&at=
                                 ▼
                       ┌───────────────────┐
                       │   L7 LB (envoy)   │ ── consistent-hash by ca_id (sharding-aware)
                       └─────────┬─────────┘
                                 │
                       ┌─────────┴─────────┐
                       │                   │
                       ▼                   ▼
              ┌──────────────┐    ┌──────────────┐
              │ cert-check   │    │ cert-check   │  ... 4–8 pods, HPA on p95
              │   service    │    │   service    │
              │  ┌────────┐  │    │  ┌────────┐  │
              │  │   L1   │  │    │  │   L1   │  │  Ristretto, 30s TTL
              │  ├────────┤  │    │  ├────────┤  │
              │  │ Bloom  │  │    │  │ Bloom  │  │  revoked serials per ca_id
              │  └────────┘  │    │  └────────┘  │
              └──────┬───────┘    └──────┬───────┘
                     │                   │
                     └─────────┬─────────┘
                               │ cache miss
                               ▼
                      ┌────────────────┐
                      │  Redis Cluster │  L2 cache, 5min TTL
                      └────────┬───────┘
                               │ L2 miss
                               ▼
                      ┌────────────────┐
                      │   PgBouncer    │  transaction-mode pool
                      └────────┬───────┘
                               │
                      ┌────────┴────────┐
                      │                 │
                      ▼                 ▼
              ┌─────────────┐    ┌─────────────┐
              │ PG primary  │◄───┤ PG replicas │  3–5 read replicas
              │ (writes)    │    │ (reads)     │  streaming replication
              └─────────────┘    └─────────────┘
                      ▲
                      │ idempotent UPSERT
                      │
              ┌───────┴────────────┐
              │ Kafka consumer pool│  ←─── topic: cert.revoked / cert.issued
              └────────────────────┘
                      ▲
                      │
              ┌───────┴────────────┐
              │       Kafka        │  partitioned by ca_id, at-least-once
              └────────┬───────────┘
                      ▲
        ┌─────────────┴──────────────┐
        │                            │
┌───────┴────────┐         ┌─────────┴────────┐
│ Webhook server │         │   CRL puller     │  periodic reconciliation
│ POST /events   │         │   per ca_id      │  safety net for lost webhooks
│  HMAC-signed   │         │                  │
└───────┬────────┘         └──────────────────┘
        │
        │
┌───────┴────────────────────────────────┐
│            100 Certificate Authorities │
└────────────────────────────────────────┘
```

**Поток чтения:** Client → LB → service → L1 → Bloom → L2 → Postgres replica → response
**Поток записи (revocation):** CA → webhook → Kafka → consumer → Postgres primary + Outbox → cache invalidation event → L1 + Bloom updates на всех инстансах
**Поток reconciliation:** scheduled puller → CRL diff → Kafka (same as live revocations)

---

## Detailed Design

### Component 1 — API Layer (Handler)

**Что делает:** валидирует входные параметры, переводит byte-hex, обращается к Store, формирует JSON-ответ. Stateless.

**Реализовано в Phase 1:**
- `GET /api/v1/check` с валидацией: `serial` (hex, even, ≤ 40 chars per RFC 5280), `ca_id` (u16), `at` (RFC 3339 с TZ)
- `GET /healthz` (liveness) — всегда 200
- `GET /readyz` (readiness) — зовёт `Store.Ping(ctx)`, 503 при недоступном backend
- Uniform JSON 4xx/5xx через `ErrorResponse{Error: ...}`
- `X-Request-ID` middleware (echoed + propagated в context + logged)
- AccessLog middleware (skip `/healthz`/`/readyz`)
- Graceful shutdown (`signal.NotifyContext` + `srv.Shutdown` с 10 s deadline)
- Full server timeouts (`Read`/`Write`/`Idle`/`ReadHeader` + `MaxHeaderBytes` 4 KB)

**Что добавится в Phase 2:**
- **Rate limiting middleware** (`golang.org/x/time/rate`, token-bucket per IP) — ~0.5 дня
- **OpenTelemetry tracing** — span на каждый запрос, propagation в Store-вызовы, `request_id` уже служит correlation key — ~1 день
- **Prometheus `/metrics`** — counter/histogram per route — ~0.5 дня

**Что вне scope:** auth (за mTLS gateway), CORS (внутренний сервис).

---

### Component 2 — Storage Layer

**Что делает:** persistent KV store с композитным ключом `(ca_id, serial)`.

**Реализовано в Phase 1 (`internal/storage/memory.go`):**
- `Store` интерфейс: `Get(ctx, caID uint16, serial []byte) (Certificate, error)`
- `Readier` интерфейс: `Ping(ctx) error`
- Типизированные ошибки `ErrNotFound`, `ErrUnavailable`
- `MemoryStore` — `map+RWMutex`, композитный ключ `(caID, string(serial))`, defensive copy при `Get`
- `storagetest.RunStoreContract(t, factory)` — переиспользуемый contract-тест (6 сценариев)

**Что добавится в Phase 2:**

**`internal/storage/postgres/`** — Postgres-impl. Подпись `Get`/`Ping` без изменений; `storagetest.RunStoreContract` прогонится против Postgres ровно теми же 6 кейсами.

Схема:
```sql
CREATE TABLE certificates (
    ca_id        SMALLINT     NOT NULL,
    serial       BYTEA        NOT NULL CHECK (octet_length(serial) BETWEEN 1 AND 20),
    not_before   TIMESTAMPTZ  NOT NULL,
    not_after    TIMESTAMPTZ  NOT NULL,
    revoked_at   TIMESTAMPTZ,
    revocation_reason SMALLINT,  -- RFC 5280 reason codes
    PRIMARY KEY (ca_id, serial)
) PARTITION BY HASH (ca_id);
```

- **HASH-партиционирование по `ca_id`** на 32 партиции
- **Subpartition по `substring(serial FROM 1 FOR 1)`** на 16 саб-партиций каждая (защита от доминирующего УЦ — см. ADR-2)
- **Покрывающий индекс** `(ca_id, serial) INCLUDE (not_before, not_after, revoked_at, revocation_reason)` → index-only scan
- **PgBouncer** в transaction mode — снижает overhead connection pool под высоким параллелизмом
- **3–5 read replicas** через streaming replication
- **Patroni** для HA primary + sync standby (RPO 0, RTO 10–30 с)

Effort: **1–2 дня** (готова обвязка тестов).

---

### Component 3 — Cache Layer

**Что делает:** многоуровневое кэширование для снижения нагрузки на Postgres.

**Реализовано в Phase 1:** нет (in-memory store сам себе кэш).

**Что добавится в Phase 2:**

**Слои:**

| Слой | Хранилище | TTL | Где живёт |
|---|---|---|---|
| L1 | Ristretto (Go in-process LRU) | 30–60 с | каждый под |
| L2 | Redis Cluster | 5–10 мин | shared |
| Negative cache | Redis | 1–2 мин | shared |
| Bloom filter | in-memory (custom or `bits-and-blooms/bloom`) | until next CRL update | каждый под |

**Декоратор:**

```go
// internal/storage/cache/cached_store.go (Phase 2)
type CachedStore struct {
    inner storage.Store
    l1    *ristretto.Cache
    l2    *redis.Client
    bloom *Filter
}

// Same Store interface — drop-in.
func (c *CachedStore) Get(ctx context.Context, caID uint16, serial []byte) (model.Certificate, error) {
    if v, ok := c.l1.Get(key); ok { return v.(model.Certificate), nil }
    if !c.bloom.MaybeContains(caID, serial) { /* fast-path "не отозван" */ }
    if v, err := c.l2.Get(ctx, key); err == nil { /* repopulate L1 */ }
    cert, err := c.inner.Get(ctx, caID, serial)
    /* populate L1 + L2 */
    return cert, err
}
```

**Инвалидация:**

Cache-key включает версию CRL per `ca_id`:
```
key = fmt.Sprintf("%d:%x:%d", caID, serial, crlVersion[caID])
```
Бамп `crl_version` (на каждое revocation event) → старые ключи естественно протухают, не нужен explicit `DEL`.

**Стампи защита:** `golang.org/x/sync/singleflight` обёртка на L2 miss → сразу за одним полётом в БД, остальные ждут результата.

**Bloom filter prewarm:** на старте пода — `/readyz` возвращает 503, пока фильтр не загружен из Redis (см. ADR-3 и Risks/R-2).

Effort: **3 дня** (декоратор + bloom + invalidation).

---

### Component 4 — Event Ingestion

**Что делает:** принимает события issuance/revocation от УЦ, применяет к БД, инвалидирует кэш.

**Реализовано в Phase 1:** нет (`MemoryStore.Seed()` захардкожен).

**Что добавится в Phase 2:**

**Топология:**

```
[CA-1] ─────┐
[CA-2] ─────┤
  ...       ├─► [Webhook server: POST /internal/ca/{ca_id}/events]
[CA-100]────┘            │
                         │ HMAC validation
                         │ idempotency key
                         ▼
                  [Outbox table in PG]  ←─ transaction with the event payload
                         │
                         │ outbox-relay worker (every 1s)
                         ▼
                  [Kafka topic: cert.events] ←── partitioned by ca_id
                         │
                  ┌──────┴──────┐
                  │             │
                  ▼             ▼
            [Consumer pool, N workers per partition]
                  │
                  │ idempotent UPSERT (see SQL below)
                  ▼
            [Postgres primary]
                  │
                  ▼
            [Cache invalidation event → all pods]
```

**Идемпотентный UPSERT** (защита от Kafka at-least-once + clock skew между публикаторами):

```sql
INSERT INTO certificates (ca_id, serial, not_before, not_after, revoked_at, revocation_reason)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (ca_id, serial) DO UPDATE
SET revoked_at = LEAST(certificates.revoked_at, EXCLUDED.revoked_at),
    revocation_reason = COALESCE(certificates.revocation_reason, EXCLUDED.revocation_reason)
WHERE certificates.revoked_at IS NULL
   OR EXCLUDED.revoked_at < certificates.revoked_at;
```

- `LEAST` обрабатывает clock skew (более раннее revocation побеждает)
- `WHERE` гарантирует, что отзыв иммутабелен после первой записи (повторная доставка — no-op)

**Outbox pattern в webhook-server:**
1. `BEGIN`
2. `INSERT INTO outbox (event) VALUES (...)` (запись события)
3. `COMMIT`
4. отдельный worker читает outbox и публикует в Kafka, помечает `published_at`

Без outbox — событие может потеряться между `INSERT INTO certificates` и `kafka.Publish` (publisher умер посередине).

**DLQ:** consumer перенаправляет битые события в `cert.events.dlq` после 5 retries. Алерт по размеру DLQ.

Effort: **4 дня** (webhook + outbox + consumer + DLQ).

---

### Component 5 — CRL Reconciliation

**Что делает:** периодически тянет CRL с каждого УЦ, сравнивает с состоянием БД, добирает разошедшееся через Kafka.

**Зачем:** safety net для потерянных вебхуков. Если CA отозвал серт, но `webhook` доставка пошла не туда, мы узнаем об этом из CRL.

**Реализовано в Phase 1:** нет.

**Что добавится в Phase 2:**

```go
// internal/events/crl_puller.go
func (p *Puller) Tick(ctx context.Context, caID uint16) error {
    crl, err := p.fetcher.FetchCRL(ctx, caID)
    if err != nil { return err }

    for _, entry := range crl.RevokedCertificates {
        existing, err := p.store.Get(ctx, caID, entry.SerialNumber)
        if errors.Is(err, storage.ErrNotFound) || existing.RevokedAt == nil {
            // CRL знает о revocation, мы — нет → публикуем event
            p.kafka.Publish(...)
        }
    }
    return nil
}
```

Расписание — `time.NewTicker(5 * time.Minute)` per `ca_id`, с jitter ±30 с чтобы не лупить все УЦ одновременно.

Effort: **1 день**.

---

### Component 6 — Observability

**Реализовано в Phase 1:**
- `slog` JSON logger
- `X-Request-ID` propagation
- AccessLog middleware с полями `method/path/query/status/dur/request_id`

**Что добавится в Phase 2:**

**Метрики Prometheus** (через `prometheus/client_golang`):

| Метрика | Type | Лейблы | Зачем |
|---|---|---|---|
| `http_requests_total` | Counter | method, route, status | RPS, error rate |
| `http_request_duration_seconds` | Histogram | method, route | latency percentiles |
| `cache_hits_total` / `cache_misses_total` | Counter | layer (L1/L2/bloom) | hit rate |
| `bloom_filter_size` | Gauge | ca_id | размер filter |
| `kafka_consumer_lag` | Gauge | topic, partition | event lag |
| `crl_lag_seconds` | Gauge | ca_id | reconciliation lag |
| `db_connections_active` | Gauge | pool | pool exhaustion alert |

**Трейсинг OpenTelemetry:**
- Tracer init в `main.go`
- Spans: handler → cache layers → store
- `request_id` из middleware = trace_id или baggage

**Логи:** `slog` уже JSON, sidecar collector (Fluent Bit / Vector) отправляет в Loki/Datadog.

**Алерты** (через Prometheus AlertManager):
- 5xx rate > 0.1 % за 5 мин
- p99 > 75 ms за 5 мин
- Kafka lag > 60 с
- CRL lag > интервал pull
- DLQ size > 0

Effort: **2 дня** (metrics + tracing skeleton; полноценные дашборды и алерты — отдельный effort).

---

## Architecture Decisions (ADRs)

Формат: **Context — Decision — Rationale — Consequences**.

### ADR-1: PostgreSQL как primary store

**Context:** 100M записей, 10k RPS read, < 100 inserts/sec, точечный доступ по PK, нужны ACID-транзакции для outbox-паттерна.

**Decision:** PostgreSQL 16, HASH-партиционирование, read replicas + PgBouncer.

**Rationale:**
- 10 ГБ данных + 6 ГБ индекса влезают в RAM одной r6i.xlarge ($150/мес) → дёшево
- B-tree point-query — самая дешёвая операция
- Зрелость экосистемы: Patroni, PITR, partman, pg_repack
- ACID нужен для outbox: insert в `certificates` + insert в `outbox` в одной транзакции — без этого revocation теряется

**Consequences:**
- ✅ Полная гибкость в схеме, миграции через `golang-migrate`
- ✅ Sync standby даёт RPO 0
- ⚠️ Vertical scaling ограничен; при > 1 млрд записей — переход на Citus или application-level шардинг (см. ADR-2 mitigation)
- ⚠️ Bloat от UPDATE revoked_at → нужен periodic `pg_repack` или partman rotation

**Альтернативы — почему отвергнуты:** см. §Alternatives Considered.

---

### ADR-2: Двухуровневое партиционирование

**Context:** ARCHITECTURE.md §1 указывает на gotcha — HASH(ca_id) изолированно коллапсирует при доминирующем УЦ (например, Let's Encrypt выпускает 70 %+ публичных сертов).

**Decision:** `PARTITION BY HASH (ca_id) → SUBPARTITION BY HASH (substring(serial FROM 1 FOR 1))`. 32 × 16 = 512 листовых партиций.

**Rationale:**
- Размазывает крупный УЦ на 16 саб-партиций → ~6 % максимум в одну
- Параллельный vacuum, параллельная запись, параллельный partition-pruning
- 512 партиций — в пределах рекомендованного для PG 16 (до 1000 без degradation)

**Consequences:**
- ✅ Hot-spot defence
- ✅ Index size per partition — управляемый ~12 МБ
- ⚠️ Сложнее миграции (любая ALTER требует replicate per partition)
- ⚠️ Запросы без `ca_id` теряют partition pruning; для нашего API — норма (`ca_id` всегда в запросе)

---

### ADR-3: Трёхслойный кэш L1 + L2 + Bloom

**Context:** Целевая p99 50 ms; round-trip к Postgres replica = ~1–2 ms; к Redis = ~0.3 ms; in-process Ristretto = ~100 ns. Дешевле всего ответить локально.

**Decision:**
- **L1 Ristretto** (in-process, 30 с TTL) — горячие хиты
- **Bloom filter** (in-process) — fast-path для «не отозван» (gateway для не-revoked сертов, минует L2/DB)
- **L2 Redis** (5 мин TTL) — shared warm cache
- **Negative cache** в L1+L2 (1–2 мин TTL) — `not_found` чтобы не штурмовать БД

**Rationale:**
- Bloom: память O(n_revoked × 1.2 байта) → 100k revoked = 120 КБ; FP 1 %. Спасает 99 % запросов на active certs от похода в Redis
- L1 → L2 разделение: L1 даёт самые горячие хиты без сетевого хопа; L2 спасает при rotation подов (warm cache не теряется)

**Consequences:**
- ✅ p99 < 50 ms достижимо
- ⚠️ Cache stampede при бампе `crl_version` или массовом expiry → `singleflight` mitigation
- ⚠️ Bloom cold start критичен (см. R-2 в Risks)
- ⚠️ Inconsistency window между L1 и L2 (до 30 с) — для PKI это, возможно, неприемлемо для высокоприоритетных сертов; см. Open Question OQ-3

---

### ADR-4: Outbox pattern для revocation events

**Context:** Без outbox пейлоад между «запись в БД» и «публикация в Kafka» теряется при падении publisher-сервера.

**Decision:** Webhook-server делает `INSERT INTO outbox` в той же транзакции, что и `INSERT/UPDATE certificates`. Отдельный outbox-relay worker читает unpublished events и кладёт в Kafka.

**Rationale:**
- Atomicity: revocation либо записана в БД И в outbox (одна транзакция), либо ничего
- Outbox-relay становится единственным publisher в Kafka → exactly-once semantics на этом конце
- При падении relay — события не теряются, лежат в outbox; restart дочитает

**Consequences:**
- ✅ No event loss
- ✅ Replay safe (Kafka consumer всё равно идемпотентный via UPSERT)
- ⚠️ Дополнительная таблица + worker
- ⚠️ Outbox-table может расти; нужен periodic cleanup (delete published_at < now() - 7 days)

---

### ADR-5: In-memory Phase 1 c full Phase 2 контрактом

**Context:** За 2-часовой scope нужно реализовать рабочий сервис. Полноценный Postgres-стек — невозможно. Но и in-memory с `map[string]Cert` — это **переписывание** при переходе на Postgres.

**Decision:** Phase 1 реализован с production-grade сигнатурами: `Store.Get(ctx, caID uint16, serial []byte) (Certificate, error)`, типизированные ошибки, defensive copy, `Readier` интерфейс. Композитный ключ `(ca_id, serial)` с самого начала.

**Rationale:**
- Phase 1 — это **prototype** контракта, не упрощённая версия
- `storagetest.RunStoreContract` гарантирует, что любая реализация ведёт себя одинаково
- Phase 2 Postgres-impl — это новый файл `internal/storage/postgres/`, **не** правки в `handler/storage/checker`

**Consequences:**
- ✅ Phase 2 — drop-in (заменить `storage.NewMemoryStore()` одной строкой в `main.go`)
- ✅ Property-based и contract тесты переиспользуются ровно как есть
- ⚠️ Чуть больше boilerplate чем минимально (defensive copy, ctx, `[]byte` вместо string) — но это не legacy, это правильно

---

## Operations

### Deployment Topology (k8s)

| Component | Replicas | Resources/pod | HPA |
|---|---|---|---|
| cert-check-service | 4 (min) – 8 (max) | 500m CPU / 256 MiB | scale on p95 latency |
| webhook-server | 2 | 250m CPU / 128 MiB | manual |
| outbox-relay | 1 (singleton, leader-elected) | 100m CPU / 64 MiB | none |
| crl-puller | 1 (singleton) | 100m CPU / 64 MiB | none |
| event-consumer | 4 (one per Kafka partition) | 500m CPU / 256 MiB | scale on consumer_lag |
| PgBouncer | DaemonSet (one per node) | 100m CPU / 64 MiB | n/a |
| Postgres primary + 3 replicas | 4 (managed by Patroni) | 4 CPU / 16 GiB | n/a |
| Redis cluster | 3 master + 3 replica | 500m CPU / 1 GiB | n/a |
| Kafka brokers | 3 | 1 CPU / 4 GiB | n/a |

**Network:** Istio service mesh для mTLS между сервисами + автоматический trace propagation.

**Storage:** EBS gp3 для Postgres (provisioned IOPS), Redis persistent storage опционально (warm cache acceptable to lose).

### Incident Response — Runbooks

| Симптом | Likely cause | First action |
|---|---|---|
| 5xx rate spike | Postgres down или connection pool exhausted | Check `db_connections_active`, scale PgBouncer, fail to replica |
| p99 spike, 2xx rate ok | Cache stampede или GC pause | Check `cache_hits_total`, `go_gc_duration_seconds` |
| Kafka consumer lag > 60 с | Consumer pod stuck or DB slow | Restart consumer; check `db_query_duration` |
| CRL lag высокий | Puller down or CA unreachable | Check puller logs; manual CRL pull |
| DLQ растёт | Кривые события от CA или схема изменилась | Inspect DLQ payload, fix consumer, replay |
| `/readyz` 503 на новом поде | Bloom prewarm not finished | Wait; if > 60 с — check Redis connectivity |

### Cost Estimate (AWS, us-east-1)

| Resource | Spec | Monthly |
|---|---|---|
| EKS cluster | 1 (3 nodes m6i.xlarge) | $400 |
| Postgres (RDS Aurora) | r6i.xlarge primary + 3 replicas | $850 |
| Redis (ElastiCache Cluster) | 3 cache.r6g.large nodes | $450 |
| Kafka (MSK) | 3 brokers kafka.m5.large | $750 |
| ALB | 1 | $25 |
| Data transfer | ~1 TB/мес | $90 |
| **Total** | | **~$2 600 / мес** |

В перерасчёте: при 10k RPS × 86 400 с/день × 30 дней ≈ 26 млрд запросов/мес → **$0.0001 / 1000 запросов**. Дёшево.

---

## Testing Strategy

| Уровень | Phase 1 | Phase 2 |
|---|---|---|
| Unit | ✅ table-driven для `Check`, parse | ⏳ +Postgres SQL builder unit tests |
| Property | ✅ 4 инварианта, 500 итераций | ⏳ no change |
| Contract | ✅ `storagetest.RunStoreContract` | ⏳ same suite against Postgres |
| Fuzz | ✅ `FuzzParseSerial`, `FuzzParseAt` | ⏳ no change |
| Integration | ⏳ | ⏳ testcontainers: PG + Redis + Kafka |
| Reliability | ⏳ | ⏳ outbox replay test, replica lag, idempotency |
| Chaos | ⏳ | ⏳ kill Redis / kill Kafka — service stays 2xx |
| Performance bench | ✅ микробенчи + vegeta | ⏳ + soak (2h) |
| Coverage | ✅ 94.4 % | ⏳ target 90 %+ with integration tests counted |

Effort на Phase 2 testing: **2 дня** на testcontainers setup + reliability/chaos scripts.

---

## Risks and Mitigations

Структурировано по матрице **Likelihood × Impact**.

### High (likely + impactful)

**R-1: Replica lag для read-your-writes.**
*Likelihood:* высокая, replica lag ~50–500 ms норма.
*Impact:* publisher только что записал revocation, читает через replica — видит старое состояние.
*Mitigation:* publisher всегда читает с primary (отдельный pool в PgBouncer); либо `pg_wait_for_lsn` после write.
*Open Question:* нужно решить до Phase 2 — добавляем ли мы это в `Store` интерфейс или поднимаем на уровне connection routing?

**R-2: Bloom filter cold start.**
*Likelihood:* при каждом scale-up.
*Impact:* первые ~10 с жизни нового пода — потенциально неверные `valid` ответы для отозванных сертов.
*Mitigation:* `/readyz` 503 до завершения preload. Реализуется в Phase 2; ARCHITECTURE.md §2 уже это документирует.

**R-3: Hot CA collapse в одну партицию.**
*Likelihood:* почти наверняка (распределение УЦ неравномерное).
*Impact:* партиция в 5–10× больше других, медленнее VACUUM, дискбалансированный I/O.
*Mitigation:* ADR-2 — двухуровневое партиционирование.

### Medium

**R-4: Cache stampede при бампе `crl_version`.**
*Likelihood:* при каждом revocation у активного УЦ.
*Impact:* latency spike на 100–500 ms.
*Mitigation:* `golang.org/x/sync/singleflight` обёртка на L2 miss.

**R-5: Kafka consumer lag при mass-revocation от скомпрометированного УЦ.**
*Likelihood:* редко, но возможно.
*Impact:* propagation lag растёт до часов.
*Mitigation:* per-partition consumer pool, авто-скейлинг по `kafka_consumer_lag`. Алерт.

**R-6: PgBouncer как SPOF.**
*Likelihood:* падение одного — высокая.
*Impact:* связные поды теряют пул.
*Mitigation:* DaemonSet (один на каждом ноду) + retry с exponential backoff в pool-config.

### Low

**R-7: Clock skew между публикаторами CA.**
*Likelihood:* норма для распределённых систем.
*Impact:* «более ранний» revocation event может прийти после «более позднего».
*Mitigation:* `LEAST()` в UPSERT (ADR-4).

**R-8: DLQ от schema mismatch.**
*Likelihood:* при добавлении нового revocation reason code.
*Impact:* пропуск событий с unknown reason.
*Mitigation:* schema registry (Confluent) + backward-compat валидация в CI.

**R-9: Postgres bloat от частых UPDATE revoked_at.**
*Likelihood:* со временем.
*Impact:* index growth, slower queries.
*Mitigation:* periodic `pg_repack`; либо переход на append-only `revocations` table + JOIN.

---

## What's in Phase 1 vs Phase 2 (Honest Status)

Жирным — то, что **готово сейчас**.

| Layer | Phase 1 (done) | Phase 2 (designed, not built) | Effort |
|---|---|---|---|
| **API contract** | ✅ `?serial`, `?ca_id`, `?at`; JSON response/error; RFC 5280 limits | — | — |
| **Domain logic** | ✅ `checker.Check`, property + table-driven | — | — |
| **Storage interface** | ✅ `Get`, `Readier`, typed errors, `[]byte` serial | Add Postgres impl behind same interface | 1–2 д |
| **In-memory backend** | ✅ MemoryStore с defensive copy, composite key | (заменяется на Postgres) | — |
| **Schema + partitioning** | — (DDL только в design doc) | `golang-migrate` + 32×16 partitions | 1 д |
| **Cache (L1/L2/Bloom)** | — | `CachedStore` decorator + Ristretto + Redis + bloom prewarm | 3 д |
| **Webhook receiver** | — | `POST /internal/ca/{ca_id}/events` + HMAC | 1 д |
| **Outbox + relay worker** | — | Outbox table + outbox-relay singleton | 2 д |
| **Kafka consumer** | — | Consumer pool + idempotent UPSERT + DLQ | 2 д |
| **CRL puller** | — | Background ticker per ca_id | 1 д |
| **Liveness / readiness** | ✅ `/healthz`, `/readyz` + `Readier` interface | (Postgres `Ping(SELECT 1)`) | 0.5 д |
| **Structured logging** | ✅ slog JSON, AccessLog, request_id | (форвард в Loki/Datadog) | 0.5 д |
| **Prometheus metrics** | — | `/metrics` endpoint + counters/histograms | 0.5 д |
| **OpenTelemetry tracing** | — | Tracer init + spans on handler/store | 1 д |
| **Rate limiting** | — | Token-bucket middleware | 0.5 д |
| **Graceful shutdown** | ✅ SIGTERM → Shutdown(10s) | (k8s preStop hook + Service draining) | 0.5 д |
| **Server timeouts** | ✅ Read/Write/Idle/Header + MaxHeaderBytes | — | — |
| **Config management** | env только `LISTEN_ADDR` | `internal/config/` пакет (DB/Redis/Kafka URLs, secrets) | 0.5 д |
| **k8s manifests** | — | Deployment, HPA, NetworkPolicy, PDB | 2 д |
| **Integration tests** | — | testcontainers (PG + Redis + Kafka) | 2 д |
| **Chaos tests** | — | Kill Redis/PG/Kafka scenarios | 1 д |
| **Soak test** | — | 2-hour vegeta sustained | 0.5 д |
| **Дашборды + алерты** | — | Grafana dashboards + AlertManager rules | 1 д |

**Total Phase 2 effort:** ~20 человеко-дней при 1 senior + 1 mid Go engineer ≈ **3 недели до production**.

---

## Alternatives Considered

### A-1: Redis как primary store (не Postgres)

**Pros:** lower latency point-query (~100 µs vs ~1 ms), built-in TTL.
**Cons:** weaker durability при failover (даже с AOF), нет multi-row транзакций для outbox, нет partition-aware индексов.
**Verdict:** отвергнуто. Cache — да, primary — нет.

### A-2: Cassandra / ScyllaDB

**Pros:** linear horizontal scaling, мощный write throughput.
**Cons:** eventual consistency несовместима с PKI use-case (нельзя терять revocation); оперативная сложность; нет ACID для outbox.
**Verdict:** отвергнуто для 100M записей. Возможный путь при росте до 10 млрд+.

### A-3: CockroachDB / TiDB

**Pros:** distributed Postgres-совместимый SQL, multi-region.
**Cons:** higher write latency, operational complexity, vendor lock-in.
**Verdict:** premature optimisation. Postgres + read replicas покрывает целевую нагрузку.

### A-4: Multi-region active-active

**Pros:** lower latency для глобальных клиентов, fault isolation.
**Cons:** cross-region Kafka mirroring, conflict resolution на revocation events, +2–3× cost.
**Verdict:** отложено до спецификации регионального SLO.

### A-5: gRPC вместо HTTP/JSON

**Pros:** более компактный wire format, типизированный контракт.
**Cons:** сложнее debug в проде, requires sidecar для browser clients, более toolchain.
**Verdict:** HTTP/JSON для v1 (simpler). gRPC возможен внутри сервиса (service-to-service).

### A-6: OCSP responder

**Pros:** стандартный протокол, поддержка в браузерах.
**Cons:** другой scope (real-time delegation), требует подписи каждого ответа.
**Verdict:** NG1 — отдельный проект.

### A-7: Event sourcing (вместо ad-hoc revocation events)

**Pros:** полный audit log, replay state.
**Cons:** для read-mostly workload — overkill; усложняет propagation latency.
**Verdict:** отвергнуто. Postgres-таблица + Kafka events достаточно.

---

## Open Questions

### OQ-1: Read-your-writes для publisher

**Question:** Должен ли webhook-receiver делать sticky read на primary после write? Через LSN tracking или через явный pool routing?
**Decision needed by:** start of Phase 2.2.
**Owner:** TBD.

### OQ-2: Cache invalidation — per-key vs version-bump

**Question:** Бампать `crl_version[ca_id]` при каждом revocation (всё протухает) vs explicit `DEL` per ключ?
**Trade-off:** version-bump проще, но даёт thunderhing herd; per-key — точнее, но требует pub/sub в Redis.
**Decision needed by:** start of Phase 2.2.

### OQ-3: SLA для high-value certs

**Question:** Для сертификатов с RevocationReason=keyCompromise — нужна ли отдельная fast-path с propagation < 1 с (минуя L1/L2 cache)?
**Trade-off:** усложняет API, но критично для compliance.
**Decision needed by:** review с security team.

### OQ-4: Audit trail retention

**Question:** Сколько хранить revocation events после применения? GDPR-релевантно для personal certs.
**Decision needed by:** review с legal team.

---

## Rollout Plan

| Phase | Содержимое | Канареечный rollout | Rollback strategy |
|---|---|---|---|
| **2.1 — Postgres** | `postgres.Store` impl, миграции, contract test passes | shadow traffic 5 % → 50 % → 100 % | вернуть `MemoryStore` через env-flag |
| **2.2 — Cache** | `CachedStore` decorator (L1 only пока) | per-pod canary с feature flag | unwrap decorator |
| **2.3 — Webhook + Kafka** | webhook-server + outbox + consumer | dual-write (manual + webhook) на 7 дней | disable webhook, manual updates |
| **2.4 — Full cache** | L2 Redis + Bloom + invalidation | shadow Redis writes 7 дней | disable Redis fetch, keep L1 |
| **2.5 — CRL puller** | reconciliation worker | observed-only mode 7 дней | disable puller, manual sync |
| **2.6 — Observability** | metrics, tracing, dashboards, alerts | always-on | — |
| **2.7 — Production cutover** | flip от Phase 1 stack | blue/green с health-check gates | DNS rollback |

Каждый шаг — отдельный PR. Каждый шаг — измеримая success criteria (SLO не нарушен × 24 ч).

---

## References

- [TASK.md](./TASK.md) — условия задания
- [README.md](./README.md) — быстрый старт + Performance + ADR
- [ARCHITECTURE.md](./ARCHITECTURE.md) — рабочие заметки про швы и пирамиду тестов
- RFC 5280 — Internet X.509 Public Key Infrastructure Certificate
- Google "Engineering Design Doc" template — внутренний документ Google, описан публично в книге "Software Engineering at Google" (Ch. 10)
- AWS Well-Architected Framework — Operational Excellence, Reliability, Performance Efficiency pillars
- Kleppmann, "Designing Data-Intensive Applications" — гл. 9 (Consistency and Consensus) для outbox/idempotency rationale

---

## Document Changelog

| Date | Author | Change |
|---|---|---|
| 2026-05-20 | dukaev | Initial draft, covers Phase 1 (implemented) + Phase 2 (designed) |
