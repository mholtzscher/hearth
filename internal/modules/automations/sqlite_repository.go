package automations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/dbsqlc"
)

// automationTimestampLayout is the fixed-width UTC layout every stored
// automation timestamp uses, so retention cutoffs compare lexicographically.
const automationTimestampLayout = "2006-01-02T15:04:05.000000000Z"

const (
	// automationDefaultPageLimit is the page size used when a caller omits one.
	automationDefaultPageLimit = 50
	// automationMaximumPageLimit bounds one definition page.
	automationMaximumPageLimit = 200
)

// SQLiteRepository owns automation definition transactions on the core's single
// connection. Every method here owns its own short transaction and never calls
// devices, NATS, or a worker.
type SQLiteRepository struct {
	database        *sql.DB
	queries         *dbsqlc.Queries
	now             func() time.Time
	newAutomationID func() (AutomationID, error)
	newRunID        func() (AutomationRunID, error)
	newSkipID       func() (AutomationSkipID, error)
}

// compile-time proof that the adapter implements the definition-management
// subset it serves today.
var _ AutomationDefinitionRepository = (*SQLiteRepository)(nil)

// NewSQLiteRepository adapts the migrated core database. Zero-valued dependency
// fields fall back to the process clock and canonical identity constructors, so
// production assembly passes an empty AutomationDependencies.
func NewSQLiteRepository(database *sql.DB, dependencies AutomationDependencies) *SQLiteRepository {
	dependencies = dependencies.withDefaults()
	return &SQLiteRepository{
		database:        database,
		queries:         dbsqlc.New(database),
		now:             dependencies.Now,
		newAutomationID: dependencies.NewAutomationID,
		newRunID:        dependencies.NewRunID,
		newSkipID:       dependencies.NewSkipID,
	}
}

// CreateAutomation persists one normalized definition at revision 1. The
// definition is normalized again here, so a caller that skipped the service
// cannot store a non-canonical document.
func (repo *SQLiteRepository) CreateAutomation(
	ctx context.Context,
	definition AutomationDefinition,
) (AutomationRecord, error) {
	normalized, err := NormalizeAutomationDefinition(definition)
	if err != nil {
		return AutomationRecord{}, err
	}
	raw, err := EncodeAutomationDefinition(normalized)
	if err != nil {
		return AutomationRecord{}, err
	}
	id, err := repo.newAutomationID()
	if err != nil {
		return AutomationRecord{}, fmt.Errorf("allocate automation ID: %w", err)
	}
	now := encodeAutomationTimestamp(repo.now())
	var record AutomationRecord
	err = repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		row, createErr := queries.CreateAutomation(ctx, dbsqlc.CreateAutomationParams{
			ID:             string(id),
			DefinitionJson: string(raw),
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if createErr != nil {
			return createErr
		}
		record, createErr = automationRecord(row)
		return createErr
	})
	return record, err
}

// GetAutomation reads one current definition, never a historical snapshot.
func (repo *SQLiteRepository) GetAutomation(ctx context.Context, id AutomationID) (AutomationRecord, error) {
	row, err := repo.queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationRecord{}, ErrAutomationNotFound
	}
	if err != nil {
		return AutomationRecord{}, err
	}
	return automationRecord(row)
}

// ListAutomations returns one ID-ascending keyset page. It reads limit+1 rows so
// HasMore is exact without a second query or a total.
func (repo *SQLiteRepository) ListAutomations(
	ctx context.Context,
	params ListAutomationsParams,
) (AutomationPage[AutomationRecord], error) {
	page := AutomationPage[AutomationRecord]{Items: []AutomationRecord{}}
	limit, err := automationPageLimit(params.Limit)
	if err != nil {
		return page, err
	}
	var rows []dbsqlc.Automation
	if params.AfterID == nil {
		rows, err = repo.queries.ListAutomationsFirstPage(ctx, dbsqlc.ListAutomationsFirstPageParams{
			Limit: int64(limit + 1),
		})
	} else {
		rows, err = repo.queries.ListAutomationsAfter(ctx, dbsqlc.ListAutomationsAfterParams{
			ID:    string(*params.AfterID),
			Limit: int64(limit + 1),
		})
	}
	if err != nil {
		return page, err
	}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		record, recordErr := automationRecord(row)
		if recordErr != nil {
			return page, recordErr
		}
		page.Items = append(page.Items, record)
	}
	return page, nil
}

// ReplaceAutomation atomically compares the expected revision, replaces the
// definition, and increments the revision by one.
func (repo *SQLiteRepository) ReplaceAutomation(
	ctx context.Context,
	id AutomationID,
	expectedRevision int64,
	definition AutomationDefinition,
) (AutomationRecord, error) {
	normalized, err := NormalizeAutomationDefinition(definition)
	if err != nil {
		return AutomationRecord{}, err
	}
	raw, err := EncodeAutomationDefinition(normalized)
	if err != nil {
		return AutomationRecord{}, err
	}
	var record AutomationRecord
	err = repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if revisionErr := checkAutomationRevision(ctx, queries, id, expectedRevision); revisionErr != nil {
			return revisionErr
		}
		row, replaceErr := queries.ReplaceAutomation(ctx, dbsqlc.ReplaceAutomationParams{
			ID:             string(id),
			DefinitionJson: string(raw),
			UpdatedAt:      encodeAutomationTimestamp(repo.now()),
			Revision:       expectedRevision,
		})
		if replaceErr != nil {
			return replaceErr
		}
		record, replaceErr = automationRecord(row)
		return replaceErr
	})
	return record, err
}

// DeleteAutomation hard-deletes one definition under the expected revision. An
// active Run is untouched: it owns a snapshot and retained history does not
// reference the definition row.
func (repo *SQLiteRepository) DeleteAutomation(ctx context.Context, id AutomationID, expectedRevision int64) error {
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if err := checkAutomationRevision(ctx, queries, id, expectedRevision); err != nil {
			return err
		}
		count, err := queries.DeleteAutomation(ctx, dbsqlc.DeleteAutomationParams{
			ID:       string(id),
			Revision: expectedRevision,
		})
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrRevisionConflict
		}
		return nil
	})
}

func (repo *SQLiteRepository) transaction(ctx context.Context, action func(*dbsqlc.Queries) error) error {
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

func checkAutomationRevision(ctx context.Context, queries *dbsqlc.Queries, id AutomationID, revision int64) error {
	row, err := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAutomationNotFound
	}
	if err != nil {
		return err
	}
	if revision <= 0 || row.Revision != revision {
		return ErrRevisionConflict
	}
	return nil
}

// automationRecord decodes one stored row and rejects a malformed identity,
// revision, timestamp, or definition instead of exposing partially trusted data.
func automationRecord(row dbsqlc.Automation) (AutomationRecord, error) {
	id, err := ParseAutomationID(row.ID)
	if err != nil {
		return AutomationRecord{}, fmt.Errorf("stored automation: %w", err)
	}
	if row.Revision < 1 {
		return AutomationRecord{}, fmt.Errorf(
			"%w: stored automation %q revision %d", ErrInvalidAutomation, id, row.Revision,
		)
	}
	definition, err := DecodeAutomationDefinition(json.RawMessage(row.DefinitionJson))
	if err != nil {
		return AutomationRecord{}, fmt.Errorf("stored automation %q: %w", id, err)
	}
	createdAt, err := decodeAutomationTimestamp(row.CreatedAt)
	if err != nil {
		return AutomationRecord{}, fmt.Errorf("stored automation %q created_at: %w", id, err)
	}
	updatedAt, err := decodeAutomationTimestamp(row.UpdatedAt)
	if err != nil {
		return AutomationRecord{}, fmt.Errorf("stored automation %q updated_at: %w", id, err)
	}
	return AutomationRecord{
		ID:         id,
		Revision:   row.Revision,
		Definition: definition,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func automationPageLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return automationDefaultPageLimit, nil
	case limit < 1 || limit > automationMaximumPageLimit:
		return 0, fmt.Errorf(
			"%w: page limit must be between 1 and %d",
			ErrInvalidAutomation, automationMaximumPageLimit,
		)
	default:
		return limit, nil
	}
}

func encodeAutomationTimestamp(value time.Time) string {
	return value.UTC().Format(automationTimestampLayout)
}

func decodeAutomationTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(automationTimestampLayout, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
