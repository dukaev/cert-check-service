# cert-check-service

HTTP-сервис для проверки статуса сертификатов по серийному номеру (Go + Docker).

**Документация:**
- [TASK.md](./TASK.md) — условия задания (оригинал)
- [DESIGN.md](./DESIGN.md) — production design doc (Google-style framework: Context → Goals → ADRs → Risks → Rollout)
- [DECISIONS.md](./DECISIONS.md) — все архитектурные решения в формате MADR (отклонения от ТЗ — с обоснованием)
- [ARCHITECTURE.md](./ARCHITECTURE.md) — рабочие заметки про швы и пирамиду тестов
- [api/openapi.yml](./api/openapi.yml) — формальная OpenAPI 3.0 спецификация
- [postman/cert-check-service.postman_collection.json](./postman/cert-check-service.postman_collection.json) — Postman коллекция для ручной проверки

---

## Соответствие TASK.md

Чтобы ревьюер мог быстро сверить — каждое требование ТЗ и где оно реализовано. Расхождения отмечены отдельно с обоснованием.

### Часть 1 — функциональные требования

| Требование ТЗ | Где реализовано | Статус |
|---|---|---|
| `GET /api/v1/check` с `serial` (required) + `at` (optional RFC3339) | `internal/handler/handler.go` | ✅ |
| Response JSON `{serial, valid, reason}` | `handler.Response` struct | ✅ |
| `reason ∈ {"", not_found, expired, revoked}` | константы в `internal/checker/checker.go` | ✅ |
| Хранилище in-memory | `internal/storage/memory.go` (`map+RWMutex`) | ✅ |
| Захардкоженные тестовые записи при старте | `MemoryStore.Seed()` | ✅ |
| Логика проверки (5 шагов из ТЗ) | `internal/checker/checker.go` — функция `Check` | ✅ |
| Валидация: serial не пустой / hex, at — RFC3339 | `parseSerial`, `parseAt` | ✅ |
| 400 при ошибках ввода с описанием | JSON `{"error":"..."}` | ✅ (см. ниже) |
| Graceful shutdown (опционально) с `context.Context` | `cmd/server/main.go` + ctx в handler | ✅ + |
| Логические пакеты (handler, storage, model) | `internal/{handler,storage,model,checker}` | ✅ + |

### Часть 2 — архитектурный эскиз

| Требование | Где |
|---|---|
| Хранение: СУБД + индексация/партиционирование | [DESIGN.md §Component 2](./DESIGN.md) + краткое ниже |
| Кэширование + инвалидация | [DESIGN.md §Component 3](./DESIGN.md) + краткое ниже |
| Масштабирование сервиса и БД | [DESIGN.md §Operations](./DESIGN.md) + краткое ниже |
| Надёжность: события о новых certs / отзывах | [DESIGN.md §Component 4–5](./DESIGN.md) + краткое ниже |

### Часть 3 — Микро-ADR

«Контекст — Альтернативы — Решение — Обоснование» — в подвале этого README.

### Сознательные отклонения от ТЗ

| Отклонение | Что было в ТЗ | Что у меня | Обоснование |
|---|---|---|---|
| `Certificate.Serial []byte` вместо `string` | `Serial string` | `Serial []byte` | Postgres BYTEA в Phase 2 требует raw bytes; делать конверсию hex↔BYTEA в двух местах хуже, чем сразу `[]byte` и hex-кодирование на boundary HTTP. Хранение hex-строки в `string` потеряет эту чистоту при переходе на БД. См. [DESIGN.md ADR-2](./DESIGN.md). |
| Добавлено поле `Certificate.CaID uint16` | в структуре только `Serial/NotBefore/NotAfter/RevokedAt` | + `CaID uint16` | Серийник уникален только в пределах УЦ — это явное требование Части 2 (100 УЦ). Композитный ключ `(CaID, Serial)` с самого начала избавляет от рефакторинга всех тестов и контрактов при миграции. CaID имеет дефолт 0, не ломает single-CA setup. |
| Эндпоинты `/healthz` + `/readyz` | ТЗ говорит про **один** эндпоинт `/api/v1/check` | добавлены 2 операционных endpoints | k8s liveness vs readiness — стандарт для production-сервиса; не business-эндпоинты, не пересекаются с спецификацией `/api/v1/check`. |
| Query-параметр `?ca_id=<uint16>` (опционально, дефолт 0) | ТЗ описывает только `serial` + `at` | добавлен | Без него невозможно различить серты двух УЦ с одинаковым серийником — это gotcha-вопрос из Части 2. Дефолт 0 сохраняет полное совместимость с ТЗ. |
| Ограничение `serial` ≤ 40 hex chars | ТЗ не упоминает | RFC 5280 §4.1.2.2 | Защита от DoS (1 МБ serial — реальный риск). RFC 5280 ставит формальный потолок 20 байтов = 40 hex. |
| Формат 400-ответа — JSON `{"error":"..."}` | «400 с описанием» (формат не задан) | JSON-uniform | Симметрично с 200-ответом (`application/json` для всего) — клиенту не нужно парсить два формата. |

---

## Быстрый старт

```bash
make run                                    # локально, по умолчанию на :8080
make docker && docker run -p 8080:8080 cert-check-service:local
```

Проверка:

```bash
curl 'http://localhost:8080/healthz'                                           # ok    [liveness]
curl 'http://localhost:8080/readyz'                                            # ready [readiness]
curl 'http://localhost:8080/api/v1/check?serial=01A2B3'                        # valid
curl 'http://localhost:8080/api/v1/check?serial=DEADBEEF'                      # revoked
curl 'http://localhost:8080/api/v1/check?serial=E0E0E0E0'                      # expired
curl 'http://localhost:8080/api/v1/check?serial=ABCD'                          # not_found (hex-валидный, но нет в store)
curl 'http://localhost:8080/api/v1/check?serial=01A2B3&at=2020-01-01T00:00:00Z' # expired (вне окна)
curl 'http://localhost:8080/api/v1/check?serial=01A2B3&ca_id=42'                # not_found (другой УЦ)
curl 'http://localhost:8080/api/v1/check?serial=NOTHEX'                         # 400 JSON error
```

## API

| Endpoint | Тип | Описание |
|---|---|---|
| `GET /api/v1/check` | business | проверка статуса сертификата |
| `GET /healthz` | liveness probe | всегда 200 если процесс жив |
| `GET /readyz` | readiness probe | 200 при доступном backend; 503 если `Store.Ping(ctx)` упал |

### `GET /api/v1/check`

**Query параметры:**

| Параметр | Тип | Обязательность | По умолчанию | Ограничения |
|---|---|---|---|---|
| `serial` | hex string | **required** | — | non-empty, even length, hex chars, ≤ 40 chars (RFC 5280 §4.1.2.2) |
| `at` | RFC3339 timestamp | optional | `time.Now().UTC()` | требует timezone (`Z` или `±HH:MM`) |
| `ca_id` | uint16 | optional | `0` | 0–65535 |

**Успешный ответ — `200 application/json`:**

```json
{ "serial": "01A2B3", "valid": true, "reason": "" }
```

`reason` ∈ `{"" | not_found | expired | revoked}`. Поле всегда присутствует (не `omitempty`).
`serial` в ответе — канонический верхний регистр.

**Ошибки:**
- `400 application/json` — невалидный ввод: `{"error": "<описание>"}`
- `405` — метод не GET
- `500 application/json` — внутренняя ошибка backend (для будущей Postgres-имплементации)

`not_found` — это **бизнес-ответ, не 404**: возвращается со статусом 200 (как и любой другой `reason`).

## Структура проекта

```
cmd/server/                        точка входа: HTTP + /healthz + /readyz + /api/v1/check
                                   + graceful shutdown + structured logging
internal/model/                    Certificate {CaID, Serial []byte, NotBefore, NotAfter, RevokedAt}
internal/checker/                  чистая функция Check(cert, time) → (valid, reason)
internal/storage/                  Store + Readier интерфейсы, MemoryStore,
                                   storagetest/RunStoreContract — контракт-тест
internal/handler/                  HTTP-обработчик, parseSerial/parseAt/parseCaID,
                                   AccessLog + WithRequestID middleware, инжектируемый Clock
loadtest/                          vegeta-таргеты и инструкция к нагрузочному тесту
.golangci.yml + .github/workflows/ линтеры + CI
Makefile                           make help для списка целей
```

## Тесты, линтеры, бенчмарки

```bash
make test           # unit + property + contract + concurrency + integration под -race
make lint           # golangci-lint (10 линтеров)
make bench          # microbenchmarks
make fuzz-serial    # fuzz parseSerial 30s
make fuzz-at        # fuzz parseAt 30s
make cover          # coverage report
make load-test      # vegeta against a running server
make docker         # build docker image
make help           # все цели
```

Эквиваленты на чистом `go`:

```bash
go test -race -count=1 ./...
go test -fuzz=FuzzParseSerial -fuzztime=30s ./internal/handler/...
go test -bench=. -benchmem ./...
golangci-lint run
```

### Покрытие тестами по слоям

| Слой | Что проверяется |
|---|---|
| `checker` | table-driven (границы окна с 1ns-точностью, `>=` для revoke, expired-побеждает-revoked) + **property-based** (4 инварианта × 500 итераций через `testing/quick`, фиксированный seed для воспроизводимости) |
| `storage` | **contract-тест** `storagetest.RunStoreContract` (6 сценариев: hit, miss, wrong-ca_id, byte-identity, ctx cancel, concurrent reads) — переиспользуется любой реализацией Store; отдельно `Seed`, `Get_returns_defensive_copy` (защита от aliasing-багов) |
| `handler` | httptest 200/400/405/500, golden JSON success-формы, golden JSON error-формы (`Content-Type` + `ErrorResponse`), ctx cancel, 100 конкурентных клиентов, `/readyz` (3 кейса: Ping nil / Ping fails / Store не satisfies Readier), AccessLog middleware (log shape + skip `/healthz` + включает `request_id` в логи), `WithRequestID` middleware (client header propagation + generated when absent), **fuzz** для парсеров `serial` и `at` |
| `cmd/server` (через handler-пакет) | graceful shutdown: `srv.Shutdown` ждёт in-flight запроса; новые соединения после shutdown — отказ |

**Покрытие: 94.4% statements** (`make cover` или `go test -coverpkg=./internal/... ./...`).
Все public-функции в `checker`/`handler`/`storage` — **100%**. Оставшиеся 5.6% — panic-ветки в тест-хелперах (`mustHex`), которые по дизайну не покрываются.

## Operations

- **Liveness vs Readiness:** `/healthz` всегда 200 при живом процессе; `/readyz` зовёт `storage.Readier.Ping(ctx)` и возвращает 503 если backend недоступен. Postgres-имплементация Phase 2 будет `Ping`'ать `SELECT 1`; MemoryStore — trivially ready.
- **Graceful shutdown:** SIGTERM/SIGINT → `srv.Shutdown` с 10-секундным таймаутом → процесс выходит после завершения in-flight запросов.
- **Structured JSON logs** через `log/slog` — каждый запрос логируется одной строкой `{method, path, query, status, dur, request_id}`. `/healthz` и `/readyz` исключены, чтобы k8s probe не топили полезные логи.
- **`X-Request-ID` middleware:** клиентский header propagируется (для retry-идемпотентности и distributed tracing), иначе генерится 16-hex random. Доступен через `handler.RequestID(ctx)` в любой точке кода.
- **Полные таймауты сервера:** `ReadHeaderTimeout` 5s, `ReadTimeout` 10s, `WriteTimeout` 10s, `IdleTimeout` 60s, `MaxHeaderBytes` 4 KB — защита от slowloris, hung connections, header-spam DoS.
- **JSON-ответы на ошибки:** 4xx/5xx возвращают `{"error": "..."}` с `Content-Type: application/json` — клиент не парсит два формата.
- **Валидация входа:** `serial` ≤ 40 hex chars (RFC 5280 §4.1.2.2), `at` строго RFC 3339 с TZ, `ca_id` uint16.
- **Конфигурация:** через переменные окружения (`LISTEN_ADDR`, по умолчанию `:8080`). Phase 2 добавит `DATABASE_URL`, `REDIS_URL`, `KAFKA_BROKERS`.

## Performance

Apple M-серия, Go 1.25.6, `GOMAXPROCS=12`. Сервер запущен нативно (Docker daemon отсутствовал на момент замера; для production-бенча — поднять контейнер и пинить ядра).

**Микробенчмарки** (`make bench`):

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

Здесь — резюме на 1 страницу. Полноценный production design doc (Google-style: Context → Goals → Detailed Design → ADRs → Risks → Rollout) — в **[DESIGN.md](./DESIGN.md)**.

### Хранение
**PostgreSQL.** Данные структурированные, почти иммутабельные (после выпуска меняется только `revoked_at`), 100M строк — типичная нагрузка для одной ноды. Доступ — точечный по PK.

Схема: `(ca_id SMALLINT, serial BYTEA, not_before, not_after, revoked_at, PRIMARY KEY(ca_id, serial))`. **Двухуровневое партиционирование:** `PARTITION BY HASH (ca_id)` × 32 + `SUBPARTITION BY HASH (substring(serial FROM 1 FOR 1))` × 16 = 512 листовых партиций. Это **защита от hot-spot УЦ**: один крупный УЦ (Let's Encrypt-style) иначе коллапсирует в одну партицию. Покрывающий индекс `(ca_id, serial) INCLUDE (not_before, not_after, revoked_at)` → index-only scan + partition pruning, без захода в heap. `serial BYTEA` вместо `TEXT` — меньше байт, нет хлопот с регистром, `CHECK (octet_length(serial) BETWEEN 1 AND 20)` по RFC 5280.

### Кэширование
- **L1** (in-process Ristretto, TTL 30–60 с) — горячие `(ca_id, serial) → status`
- **L2** (Redis cluster, TTL 5–10 мин) — широкий warm cache
- **Negative cache** для `not_found` с коротким TTL (1–2 мин)
- **Bloom filter** по отозванным серийникам на каждом инстансе — решает 99 % проверок без обращения к Redis/БД; `/readyz` MUST gate на prewarm

**Инвалидация:** publisher пишет revocation в БД и публикует `cert.revoked` в Kafka; consumer на каждом инстансе делает `cache.Delete + bloom.Add`. Ключ кэша включает `crl_version` per `ca_id` — бамп версии естественно протухает старые ключи. TTL на L2 короткий, как страховка. Защита от cache stampede — `golang.org/x/sync/singleflight` на L2 miss.

### Масштабирование
- **Сервис stateless** — N подов за L7-балансировщиком, автоскейлинг по p95 latency. Один под держит несколько тысяч RPS на ядро (Часть 1 на одной ноде уже даёт ~10k RPS), 10k под целевую — 4–8 подов с запасом
- **Sharding-aware routing**: балансировщик хеширует по `ca_id` → инстанс. Bloom-фильтры становятся «горячими» по конкретным УЦ, hit-rate растёт. Это объясняет, зачем `?ca_id=` в API.
- **БД:** read replicas (3–5), PgBouncer (transaction mode, DaemonSet) перед primary. Запись (revoke/issue) — только в primary. С учётом кэша к репликам приходит лишь 1–5 % miss-трафика
- Двухуровневые HASH-партиции подготовили почву для дальнейшего шардинга (Citus / app-level по `ca_id mod N`)

### Надёжность
Два источника событий:
- **Inbound от УЦ → Kafka → consumer → Postgres:** вебхуки `POST /internal/ca/{ca_id}/events` с HMAC, либо CRL/delta-CRL по расписанию. Идемпотентные `INSERT … ON CONFLICT … DO UPDATE` с `LEAST(existing, new)` (защита от clock skew между публикаторами). Publisher использует **outbox pattern** (запись в БД + событие в Kafka в одной транзакции через outbox-таблицу)
- **Периодическая сверка с CRL** — защищает от потерянных вебхуков (eventual consistency safety net)

**SLO propagation:** p99 < 30 с при штатной работе; ≤ интервал CRL pull (5–15 мин) при потере событий. Мониторинг: метрика `crl_lag_seconds` per `ca_id`, алерт > X мин. DLQ для битых событий. `/readyz` проверяет доступность БД, Redis и свежесть Kafka.

---

## Часть 3. Микро-ADR (TL;DR)

**Контекст:** in-memory store должен выдержать 10k RPS и не помешать миграции на Postgres в Phase 2.

**Альтернативы:** `sync.Map`, `map+RWMutex` с композитным ключом, sharded map с per-CA mutex.

**Решение:** `map[memKey]Certificate` под `sync.RWMutex`, где `memKey = (caID uint16, serial-as-bytes)`, плюс типизированные ошибки (`ErrNotFound`, `ErrUnavailable`).

**Обоснование:** бенчмарк `Get` под `RunParallel` показывает **173 ns/op, 0 allocs** — `RWMutex` достаточен на read-heavy workload; `sync.Map` оптимизирован под mixed read/write (не наш случай); композитный ключ с самого начала делает переход на Postgres drop-in (тот же `(ctx, caID, serial)`-контракт), `storagetest.RunStoreContract` прогонится без правок.

**Полная версия с альтернативами, последствиями и mitigation** — в [DECISIONS.md ADR-005](./DECISIONS.md#adr-005-in-memory-store-как-maprwmutex-с-композитным-ключом). Все остальные архитектурные решения и сознательные отклонения от ТЗ — там же в формате MADR.
