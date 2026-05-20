# cert-check-service

Сервис проверки статуса сертификатов (Go + Docker).

См. [TASK.md](./TASK.md) для условий задания.

## Быстрый старт

```bash
# локально
go run ./cmd/server

# в Docker
docker build -t cert-check-service .
docker run --rm -p 8080:8080 cert-check-service
```

После запуска:

```bash
curl 'http://localhost:8080/healthz'
curl 'http://localhost:8080/api/v1/check?serial=01A2B3'
curl 'http://localhost:8080/api/v1/check?serial=01A2B3&at=2026-01-01T00:00:00Z'
```

## Структура

```
cmd/server/         — точка входа
internal/handler/   — HTTP-обработчики
internal/storage/   — интерфейс и in-memory реализация хранилища
internal/model/     — доменные типы
```

## Архитектурный эскиз (Часть 2)

_Заполняется по итогам Части 2 задания._

## Микро-ADR (Часть 3)

_Заполняется по итогам Части 3 задания._
