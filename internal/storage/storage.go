package storage

import "github.com/dukaev/cert-check-service/internal/model"

// Store описывает доступ к хранилищу сертификатов.
// Production-имплементация (Postgres и др.) подключается через тот же интерфейс.
type Store interface {
	Get(serial string) (model.Certificate, bool)
}
