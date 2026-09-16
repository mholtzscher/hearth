// Package sqlite implements the automations persistence seams over one migrated
// SQLite database: transaction boundaries, query execution, generated dbsqlc row
// types, row mapping, and the query sources under dbqueries. It imports the
// automations domain, never the reverse, so domain policy and the pure rules it
// calls — definition normalization, trigger matching, Fact summaries, Run
// snapshot construction, and completion validation — never depend on SQL row
// types, database/sql, or a generated query package.
//
// Transactions never call devices, NATS, or workers. Admission commits matching
// receipts, Runs, Skips, and initial Steps atomically, and the Service starts Run
// workers only after that commit returns.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// Compile-time proof that one repository satisfies the complete
// domain-oriented persistence seam and its narrower definition-management
// capability.
var (
	_ automations.Repository           = (*AutomationRepository)(nil)
	_ automations.DefinitionRepository = (*AutomationRepository)(nil)
)

// AutomationRepository persists automation definitions, admission, execution,
// and history. Run and Skip identities are minted inside their admission
// transaction; definition identity is allocated before its creation transaction.
type AutomationRepository struct {
	database        *sql.DB
	queries         *dbsqlc.Queries
	now             func() time.Time
	newAutomationID func() (automations.AutomationID, error)
	newRunID        func() (automations.RunID, error)
	newSkipID       func() (automations.SkipID, error)
}

// NewAutomationRepository uses a migrated Core database. Zero-valued
// dependencies default to the process clock and canonical identity constructors.
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

// transaction is the single transaction owner. It runs one action against a
// transaction-bound query set and commits only when the action returns nil.
func (repo *AutomationRepository) transaction(
	ctx context.Context,
	action func(*dbsqlc.Queries) error,
) error {
	transaction, err := repo.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("automation transaction begin: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err = action(repo.queries.WithTx(transaction)); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("automation transaction commit: %w", err)
	}
	return nil
}
