# Architecture Decision Records

Все значимые архитектурные решения проекта в формате **MADR** (Markdown ADR): `Context — Decision Drivers — Considered Options — Decision Outcome — Consequences`.

Каждое решение явно отмечает, **отклоняется ли оно от TASK.md** и какой trade-off лежит в основе.

| ADR | Заголовок | Status | Phase | Отклонение от ТЗ? |
|---|---|---|---|---|
| [ADR-001](#adr-001-serial-как-byte-вместо-string) | Serial как `[]byte` вместо `string` | Accepted | Phase 1 | **Да** |
| [ADR-002](#adr-002-добавление-caid-в-certificate) | Добавление `CaID` в `Certificate` | Accepted | Phase 1 | **Да** |
| [ADR-003](#adr-003-разделение-healthz-vs-readyz) | Разделение `/healthz` vs `/readyz` | Accepted | Phase 1 | **Да** (extension) |
| [ADR-004](#adr-004-query-параметр-ca_id) | Query-параметр `?ca_id=` | Accepted | Phase 1 | **Да** (extension) |
| [ADR-005](#adr-005-in-memory-store-как-maprwmutex-с-композитным-ключом) | In-memory store как `map+RWMutex` с композитным ключом | Accepted | Phase 1 | Нет |
| [ADR-006](#adr-006-postgresql-как-primary-store-в-phase-2) | PostgreSQL как primary store в Phase 2 | Accepted | Phase 2 | Нет |
| [ADR-007](#adr-007-двухуровневое-партиционирование-hashca_id--subpartitionserial0) | Двухуровневое партиционирование HASH(ca_id) × SUBPARTITION(serial[0]) | Accepted | Phase 2 | Нет |
| [ADR-008](#adr-008-outbox-pattern-для-revocation-events) | Outbox pattern для revocation events | Accepted | Phase 2 | Нет |

---

## ADR-001: Serial как `[]byte` вместо `string`

**Status:** Accepted · **Phase:** 1 · **TASK.md compliance:** ⚠️ deviation

### Context

TASK.md явно задаёт структуру:
```go
type Certificate struct {
    Serial    string
    NotBefore time.Time
    NotAfter  time.Time
    RevokedAt *time.Time
}
```

При этом Часть 2 ТЗ описывает миграцию на PostgreSQL, который хранит serial как `BYTEA` (binary). Если оставить `Serial string` с hex-кодированием:
- запись в БД требует `hex.DecodeString` → `[]byte`
- чтение из БД требует `hex.EncodeToString` → `string`
- кэш Redis повторяет ту же конверсию

Это **две точки конверсии** на каждый запрос вместо одной (на HTTP-границе).

### Decision Drivers

- Phase 2 migration cleanness (drop-in замена реализации Store)
- Производительность hot path (минимум аллокаций)
- Соответствие букве ТЗ Часть 1

### Considered Options

| Опция | Описание |
|---|---|
| A. `Serial string` (literal-match ТЗ) | hex-строка в модели, конверсия в storage-impl |
| B. `Serial []byte` (текущий выбор) | raw bytes в модели, hex-кодирование только на HTTP-границе |
| C. Гибрид: `Certificate.Serial string` для API + `storage.Record.serial []byte` внутри | строгий compliance ТЗ + Phase 2 cleanness |

### Decision Outcome

Выбран **вариант B** (`Serial []byte`).

### Consequences

**Положительные:**
- Phase 2 migration — drop-in: новая `PostgresStore` реализует тот же `Store` интерфейс с `[]byte` без конверсий.
- `MemoryStore.Get` стал **0 B/op** (было 5 B/op) — Go оптимизирует `string(bytes)` как map-key alloc-free.
- Vegeta load test показывает измеримое улучшение p99 (24 ms → 19.5 ms).
- Один путь конверсии: `hex.DecodeString` в `parseSerial`, `hex.EncodeToString` в `Handler.Check` response.

**Отрицательные:**
- **Отклонение от буквы ТЗ** — структура `Certificate` отличается от прописанной. Строгий ревьюер заметит.
- Тестовые фикстуры более многословны: `mustHex("01A2B3")` вместо `"01A2B3"`.
- Migration cost при будущем откате обратно к `string` — нетривиальный.

### Mitigation

- Отклонение явно задокументировано в README §«Сознательные отклонения».
- ADR описан здесь с полным rationale.
- API наружу остаётся hex-строковым — публичный контракт не сломан.

### Alternative considered: Гибрид (Вариант C)

Идея: оставить `Certificate.Serial string` под ТЗ, ввести отдельный `storage.Record{caID, serial []byte, cert}` внутри storage-слоя.

**Почему отвергнут:** удвоение моделей и многословие в тестах не оправданы. ТЗ Часть 2 уже описывает 100 УЦ — `CaID` в модели всё равно нужен (см. ADR-002), и компромисс на гибрид частично теряет цель.

---

## ADR-002: Добавление `CaID` в `Certificate`

**Status:** Accepted · **Phase:** 1 · **TASK.md compliance:** ⚠️ deviation

### Context

TASK.md Часть 2 описывает архитектуру для **100 УЦ**. В PKI серийный номер X.509 уникален **только в пределах одного УЦ** — два разных УЦ могут (легально) выпустить сертификаты с одинаковым `serial`. Это не коллизия, это норма.

Однако структура `Certificate` в Части 1 не содержит `CaID`. Если оставить так, Store-интерфейс будет:
```go
Get(serial string) (Certificate, bool)
```
а в Phase 2 потребует рефакторинга в:
```go
Get(ctx, caID uint16, serial []byte) (Certificate, error)
```

Это **breaking change** интерфейса со всеми последствиями: правки в handler, во всех тестах, в contract-suite.

### Decision Drivers

- Phase 2 multi-CA addressability (требование из Часть 2)
- Стабильность интерфейса `Store` через границу Phase 1 → Phase 2
- Соответствие букве ТЗ Часть 1

### Considered Options

| Опция | Описание |
|---|---|
| A. Без `CaID` (literal-match ТЗ) | поле появляется в Phase 2 — breaking change Store |
| B. `CaID uint16` в `Certificate` с дефолтом 0 | поле есть с самого начала, single-CA setup работает identical к ТЗ |

### Decision Outcome

Выбран **вариант B** (`CaID` с дефолтом 0).

### Consequences

**Положительные:**
- Store-интерфейс **стабилен** через переход Phase 1 → 2.
- `?ca_id=` query параметр уже работает (см. ADR-004) — gotcha-вопрос «два УЦ с одним serial» закрыт.
- `storagetest.RunStoreContract` уже включает кейс `Get_wrong_ca_id_returns_ErrNotFound` — гарантия корректности при росте до multi-CA.

**Отрицательные:**
- **Отклонение от буквы ТЗ** — поле, которого в Части 1 нет.
- Чуть больше boilerplate в тестовых фикстурах (`{CaID: 0, ...}`).

### Mitigation

- Дефолт `CaID = 0` сохраняет полную семантическую совместимость с single-CA сценарием ТЗ.
- Документировано в README §«Сознательные отклонения» и здесь.

---

## ADR-003: Разделение `/healthz` vs `/readyz`

**Status:** Accepted · **Phase:** 1 · **TASK.md compliance:** ⚠️ deviation (extension)

### Context

TASK.md говорит «HTTP-сервис с **одним** эндпоинтом» — `GET /api/v1/check`. Однако production-сервис в k8s требует:
- **Liveness probe** — «процесс жив, не deadlocked» (рестарт при провале)
- **Readiness probe** — «процесс готов обслуживать трафик» (out-of-rotation при провале)

Если объединить — например, единый `/healthz` который проверяет backend — то Redis blip убивает все поды одновременно (cascading failure). Если только liveness — k8s шлёт трафик в pod, у которого Postgres мёртв.

### Decision Drivers

- Безопасное поведение в k8s rolling update / scaling
- Корректное reaction на partial outage (Redis down ≠ kill pods)
- TASK.md «один эндпоинт»

### Considered Options

| Опция | Описание |
|---|---|
| A. Только `/api/v1/check` (literal-match) | k8s liveness probe указывает на бизнес-эндпоинт — не идиоматично |
| B. Один `/healthz` (обычная практика) | без разделения liveness/readiness, риск cascading failure |
| C. `/healthz` (liveness, всегда 200) + `/readyz` (readiness, зовёт `Store.Ping`) | k8s-стандарт, безопасное degradation |

### Decision Outcome

Выбран **вариант C** — оба эндпоинта.

### Consequences

**Положительные:**
- k8s probe настройки идиоматичны.
- При Redis/Postgres blip — `/readyz` 503, k8s выводит pod из rotation, **не убивая**. Когда backend восстанавливается — `/readyz` 200, pod возвращается в Service.
- `Readier` интерфейс в `storage` — пустой шов для Phase 2 (Postgres-impl делает `SELECT 1`).

**Отрицательные:**
- **Отклонение от ТЗ «один эндпоинт»**. Защита: операционные эндпоинты ≠ бизнес-эндпоинты.

### Mitigation

- Бизнес-эндпоинт ровно один (`/api/v1/check`). `/healthz` и `/readyz` — операционная инфраструктура, явно отделена в README.
- AccessLog middleware skip-list для `/healthz` и `/readyz` — k8s probe не топят логи.

---

## ADR-004: Query-параметр `?ca_id=`

**Status:** Accepted · **Phase:** 1 · **TASK.md compliance:** ⚠️ deviation (extension)

### Context

TASK.md описывает только два query параметра: `serial` и `at`. Однако ADR-002 фиксирует `CaID` в модели. Если query не позволяет клиенту указать `ca_id`, то two CAs with same serial становятся неразличимы — gotcha-вопрос из Части 2 не закрыт.

### Considered Options

| Опция | Описание |
|---|---|
| A. Не добавлять `?ca_id=` (literal-match) | внутренне CaID есть, но API его не использует — half-baked |
| B. `?ca_id=<uint16>` опционально, дефолт 0 | спецификация ТЗ работает identical для single-CA |

### Decision Outcome

Выбран **вариант B**.

### Consequences

**Положительные:**
- Multi-CA disambiguation работает.
- Single-CA setup (default 0) семантически identical к ТЗ.
- Sharding-aware routing в Phase 2 имеет точку входа для hash by ca_id.

**Отрицательные:**
- **Лёгкое отклонение от ТЗ** (добавлен необязательный параметр).

### Mitigation

- Опциональный параметр не ломает существующие запросы.
- Документирован в README API table.

---

## ADR-005: In-memory store как `map+RWMutex` с композитным ключом

**Status:** Accepted · **Phase:** 1 · **TASK.md compliance:** ✅ corresponds

> Это формальная версия **Часть 3 ТЗ** (Микро-ADR).

### Context

Часть 1 требует in-memory store, который выдержит производственно-релевантную нагрузку (10k RPS) и при этом не поломает миграцию на Postgres из Части 2.

### Considered Options

| Опция | Описание |
|---|---|
| A. `sync.Map[string]Certificate` | простой ключ-строка, lock-free reads |
| B. **`map[memKey]Certificate` под `sync.RWMutex`**, где `memKey = struct{caID uint16; serial string}` | RWMutex, композитный ключ |
| C. Sharded map с per-CA mutex | partitioning внутри store |

### Decision Outcome

Выбран **вариант B**.

### Decision Drivers — measured

- Бенчмарк `MemoryStore.Get` под `RunParallel`: **173 ns/op, 0 allocs**.
- Vegeta load test: **9993 req/s sustained, p99 = 19.5 ms** — целевые 10k RPS из ТЗ достижимы.

### Consequences

**Положительные:**
- На read-heavy workload с редкой записью contention на `RWMutex` пренебрежимо малый — `sync.Map` не дал бы выигрыша (оптимизирован под равное чтение/запись).
- Sharded map (вариант C) даёт выигрыш только когда внутренний contention существенен — у нас не существенен.
- **Композитный ключ + `[]byte` serial с самого начала** — Postgres-impl drop-in (см. ADR-001, ADR-002).
- `storagetest.RunStoreContract` прогонится против Postgres без правок.

**Отрицательные:**
- Цена возможной ошибки низкая: если на нагрузке выявится contention, замена на sharded-вариант — внутри `MemoryStore`, не ломает интерфейс.

---

## ADR-006: PostgreSQL как primary store в Phase 2

**Status:** Accepted · **Phase:** 2 (design only) · **TASK.md compliance:** ✅ corresponds

### Context

100M записей, 10k RPS read, ~30 writes/sec, точечный доступ по PK, нужны ACID-транзакции для outbox-паттерна (ADR-008).

### Considered Options

| Опция | Описание |
|---|---|
| A. PostgreSQL 16 + partitioning + read replicas | reliable, mature, ACID, дёшево |
| B. Redis primary | low-latency, но weaker durability при failover |
| C. Cassandra/ScyllaDB | linear scaling, но eventual consistency проблематична для PKI |
| D. CockroachDB/TiDB | distributed Postgres-compat, но operational overhead |

### Decision Outcome

Выбран **вариант A** — PostgreSQL.

### Consequences

**Положительные:**
- 10 ГБ данных + 6 ГБ индекса влезают в RAM одной r6i.xlarge (~$150/мес).
- B-tree point-query — самая дешёвая операция.
- Зрелая экосистема: Patroni, PITR, partman, pg_repack.
- ACID для outbox-паттерна (insert в certificates + insert в outbox в одной транзакции).

**Отрицательные:**
- Vertical scaling ограничен; при > 1 млрд записей — переход на Citus или application-level шардинг.
- Bloat от UPDATE revoked_at → нужен periodic pg_repack.

### Mitigation

- Двухуровневое партиционирование (ADR-007) даёт запас по горизонтальному шардингу.

---

## ADR-007: Двухуровневое партиционирование HASH(ca_id) × SUBPARTITION(serial[0])

**Status:** Accepted · **Phase:** 2 (design only) · **TASK.md compliance:** ✅ corresponds

### Context

ARCHITECTURE.md §1 указывает на gotcha: HASH(ca_id) изолированно коллапсирует при доминирующем УЦ (например, Let's Encrypt выпускает 70 %+ публичных сертов). Один УЦ → одна партиция → горячая точка.

### Considered Options

| Опция | Описание |
|---|---|
| A. HASH(ca_id) только | hot-spot risk при неравномерном распределении |
| B. HASH(ca_id) × SUBPARTITION HASH(substring(serial FROM 1 FOR 1)) | размазывает крупного УЦ на 16 саб-партиций |
| C. Range-партиционирование по серийнику | сложно при автогенерируемых serial без natural ordering |

### Decision Outcome

Выбран **вариант B**: 32 × 16 = 512 листовых партиций.

### Consequences

**Положительные:**
- Hot-spot defence.
- Index size per partition управляемый ~12 МБ.
- Параллельный VACUUM, параллельная запись.

**Отрицательные:**
- Сложнее миграции (любой ALTER реплицируется per partition).
- Запросы без `ca_id` теряют partition pruning — для нашего API не проблема (CA всегда в запросе).

---

## ADR-008: Outbox pattern для revocation events

**Status:** Accepted · **Phase:** 2 (design only) · **TASK.md compliance:** ✅ corresponds

### Context

Без outbox payload между «запись в БД» и «публикация в Kafka» теряется при падении publisher-сервера. Это **тихая потеря revocation** — данные в БД есть, события в Kafka нет, кэш на других подах не инвалидирован, серт продолжает считаться valid.

### Considered Options

| Опция | Описание |
|---|---|
| A. `INSERT INTO certs` → `kafka.Publish` (sequential) | потеря события при падении между |
| B. Outbox table: `INSERT certs + INSERT outbox` в одной транзакции; relay-worker читает outbox | atomicity, replay-safe |
| C. Change Data Capture (Debezium) | сложная инфраструктура; даёт всё то же, что outbox |

### Decision Outcome

Выбран **вариант B**.

### Consequences

**Положительные:**
- Atomicity: revocation либо записана в БД И в outbox, либо ничего.
- Replay-safe (Kafka consumer уже идемпотентный через `LEAST()` UPSERT).
- При падении relay — события не теряются, лежат в outbox; restart дочитает.

**Отрицательные:**
- Дополнительная таблица + worker.
- Outbox-table может расти; нужен periodic cleanup (`DELETE published_at < now() - 7 days`).

---

## Использованный формат

Этот документ написан в формате **MADR** (Markdown Architecture Decision Records) — стандарт ADR в индустрии с 2018 года.

Каждое ADR содержит:
- **Status:** Proposed / Accepted / Deprecated / Superseded
- **Context:** что подтолкнуло к решению
- **Decision Drivers:** ключевые критерии выбора
- **Considered Options:** все рассмотренные варианты (не только выбранный)
- **Decision Outcome:** что выбрано
- **Consequences:** последствия — положительные И отрицательные
- **Mitigation** (опционально): как смягчены отрицательные последствия

Format reference: <https://adr.github.io/madr/>.
