// Package sqlite implements the automations persistence seams over one migrated
// SQLite database: transaction boundaries, query execution, generated dbsqlc row
// types, row mapping, and the query sources under dbqueries. It imports the
// automations domain, never the reverse.
//
// Transactions never call devices, NATS, or workers.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// Compile-time proof that one repository satisfies the complete persistence seam
// and its narrower definition-management capability.
var (
	_ automations.Repository           = (*AutomationRepository)(nil)
	_ automations.DefinitionRepository = (*AutomationRepository)(nil)
)

// AutomationRepository persists automation definitions, admission, execution, and
// history. Run and Skip identities are minted inside their admission transaction.
type AutomationRepository struct {
	database        *sql.DB
	queries         *dbsqlc.Queries
	now             func() time.Time
	newAutomationID func() (automations.AutomationID, error)
	newRunID        func() (automations.RunID, error)
	newSkipID       func() (automations.SkipID, error)
}

// NewAutomationRepository uses a migrated Core database.
func NewAutomationRepository(
	database *sql.DB,
	dependencies automations.Dependencies,
) *AutomationRepository {
	dependencies = dependencies.WithDefaults()
	return &AutomationRepository{
		database:        database,
		queries:         dbsqlc.New(database),
		now:             dependencies.Now,
		newAutomationID: dependencies.NewAutomationID,
		newRunID:        dependencies.NewRunID,
		newSkipID:       dependencies.NewSkipID,
	}
}

// transaction is the single transaction owner, committing only when the action returns nil.
func (repo *AutomationRepository) transaction(
	ctx context.Context,
	action func(*dbsqlc.Queries) error,
) error {
	return repo.transactionWithTx(ctx, func(queries *dbsqlc.Queries, _ *sql.Tx) error {
		return action(queries)
	})
}

func (repo *AutomationRepository) transactionWithTx(
	ctx context.Context,
	action func(*dbsqlc.Queries, *sql.Tx) error,
) error {
	transaction, err := repo.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("automation transaction begin: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err = action(repo.queries.WithTx(transaction), transaction); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("automation transaction commit: %w", err)
	}
	return nil
}
