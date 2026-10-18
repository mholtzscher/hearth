// Package sqlite persists Adapter runtime, health, and publication evidence.
package sqlite

import (
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

var (
	_ adapters.RuntimeRepository      = (*AdapterRepository)(nil)
	_ adapters.ReadRepository         = (*AdapterRepository)(nil)
	_ adapters.HealthPublicationStore = (*AdapterRepository)(nil)
)

type AdapterRepository struct {
	database *sql.DB
	queries  *dbsqlc.Queries
}

func NewAdapterRepository(database *sql.DB) *AdapterRepository {
	return &AdapterRepository{database: database, queries: dbsqlc.New(database)}
}

func AdapterStores(repository *AdapterRepository) adapters.Stores {
	return adapters.Stores{Runtimes: repository, Reads: repository}
}

func nullableText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
