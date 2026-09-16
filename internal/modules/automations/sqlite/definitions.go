package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// CreateAutomation persists a definition at revision 1, normalizing it even
// when the caller bypasses the service.
func (repo *AutomationRepository) CreateAutomation(
	ctx context.Context,
	definition automations.Definition,
) (automations.Record, error) {
	normalized, err := automations.NormalizeDefinition(definition)
	if err != nil {
		return automations.Record{}, err
	}
	raw, err := automations.EncodeDefinition(normalized)
	if err != nil {
		return automations.Record{}, err
	}
	id, err := repo.newAutomationID()
	if err != nil {
		return automations.Record{}, fmt.Errorf("allocate automation ID: %w", err)
	}
	now := encodeAutomationTimestamp(repo.now())
	var record automations.Record
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
func (repo *AutomationRepository) GetAutomation(
	ctx context.Context,
	id automations.AutomationID,
) (automations.Record, error) {
	row, err := repo.queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return automations.Record{}, automations.ErrAutomationNotFound
	}
	if err != nil {
		return automations.Record{}, err
	}
	return automationRecord(row)
}

// ListEnabledAutomations reads every currently enabled definition in ascending
// Automation ID order for the Service's admission State pre-read.
func (repo *AutomationRepository) ListEnabledAutomations(
	ctx context.Context,
) ([]automations.Record, error) {
	rows, err := repo.queries.ListAllAutomations(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]automations.Record, 0, len(rows))
	for _, row := range rows {
		record, recordErr := automationRecord(row)
		if recordErr != nil {
			return nil, recordErr
		}
		if record.Definition.Enabled {
			records = append(records, record)
		}
	}
	return records, nil
}

// ListAutomations returns one ID-ascending keyset page. It reads limit+1 rows so
// HasMore is exact without a second query or a total.
func (repo *AutomationRepository) ListAutomations(
	ctx context.Context,
	params automations.ListAutomationsParams,
) (automations.Page[automations.Record], error) {
	page := automations.Page[automations.Record]{Items: []automations.Record{}}
	limit, err := automations.PageLimit(params.Limit)
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

// ReplaceAutomation normalizes and replaces the definition under the expected
// revision, atomically incrementing the revision by one.
func (repo *AutomationRepository) ReplaceAutomation(
	ctx context.Context,
	id automations.AutomationID,
	expectedRevision int64,
	definition automations.Definition,
) (automations.Record, error) {
	normalized, err := automations.NormalizeDefinition(definition)
	if err != nil {
		return automations.Record{}, err
	}
	raw, err := automations.EncodeDefinition(normalized)
	if err != nil {
		return automations.Record{}, err
	}
	var record automations.Record
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
func (repo *AutomationRepository) DeleteAutomation(
	ctx context.Context,
	id automations.AutomationID,
	expectedRevision int64,
) error {
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
			return automations.ErrRevisionConflict
		}
		return nil
	})
}

// checkAutomationRevision reports the current revision mismatch as
// [automations.ErrRevisionConflict], and a missing definition as
// [automations.ErrAutomationNotFound], inside the caller's transaction.
func checkAutomationRevision(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.AutomationID,
	revision int64,
) error {
	row, err := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return automations.ErrAutomationNotFound
	}
	if err != nil {
		return err
	}
	if revision <= 0 || row.Revision != revision {
		return automations.ErrRevisionConflict
	}
	return nil
}
