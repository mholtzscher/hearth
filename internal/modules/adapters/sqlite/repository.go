// Package sqlite persists Adapter runtime, health, publication, and Function evidence.
package sqlite

import (
	"context"
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

var (
	_ adapters.RuntimeRepository      = (*AdapterRepository)(nil)
	_ adapters.ReadRepository         = (*AdapterRepository)(nil)
	_ adapters.HealthPublicationStore = (*AdapterRepository)(nil)
	_ adapters.FunctionRepository     = (*AdapterRepository)(nil)
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

func (repository *AdapterRepository) functionTransaction(ctx context.Context, work func(*dbsqlc.Queries) error) error {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = work(repository.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}
