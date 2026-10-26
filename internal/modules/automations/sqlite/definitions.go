package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// CreateAutomation persists a definition at revision 1, normalizing it even when
// the caller bypasses the service.
func (repo *AutomationRepository) CreateAutomation(
	ctx context.Context,
	definition automations.Definition,
) (automations.Record, error) {
	_, raw, err := automations.NormalizeAndEncodeDefinition(definition)
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
		if createErr := queries.CreateDefinitionRevision(ctx, dbsqlc.CreateDefinitionRevisionParams{
			AutomationID: string(id), Revision: 1, DefinitionJson: string(raw),
		}); createErr != nil {
			return createErr
		}
		if createErr := queries.CreateAutomation(ctx, dbsqlc.CreateAutomationParams{
			ID: string(id), CreatedAt: now, UpdatedAt: now,
		}); createErr != nil {
			return createErr
		}
		row, createErr := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
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

// ListEnabledAutomations reads every currently enabled definition in ascending Automation ID order.
func (repo *AutomationRepository) ListEnabledAutomations(
	ctx context.Context,
) ([]automations.Record, error) {
	rows, err := repo.queries.ListEnabledAutomations(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]automations.Record, 0, len(rows))
	for _, row := range rows {
		record, recordErr := automationRecord(row)
		if recordErr != nil {
			return nil, recordErr
		}
		records = append(records, record)
	}
	return records, nil
}

// ListAutomations returns one ID-ascending keyset page, reading limit+1 rows so HasMore is exact.
func (repo *AutomationRepository) ListAutomations(
	ctx context.Context,
	params automations.ListAutomationsParams,
) (automations.Page[automations.Record], error) {
	page := automations.Page[automations.Record]{Items: []automations.Record{}}
	limit, err := automations.PageLimit(params.Limit)
	if err != nil {
		return page, err
	}
	var rows []dbsqlc.AutomationDefinition
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

// ReplaceAutomation normalizes and replaces the definition under the expected revision.
func (repo *AutomationRepository) ReplaceAutomation(
	ctx context.Context,
	id automations.AutomationID,
	expectedRevision int64,
	definition automations.Definition,
) (automations.Record, error) {
	_, raw, err := automations.NormalizeAndEncodeDefinition(definition)
	if err != nil {
		return automations.Record{}, err
	}
	var record automations.Record
	err = repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if revisionErr := checkAutomationRevision(ctx, queries, id, expectedRevision); revisionErr != nil {
			return revisionErr
		}
		if replaceErr := queries.CreateDefinitionRevision(ctx, dbsqlc.CreateDefinitionRevisionParams{
			AutomationID: string(id), Revision: expectedRevision + 1, DefinitionJson: string(raw),
		}); replaceErr != nil {
			return replaceErr
		}
		if replaceErr := queries.ReplaceAutomation(ctx, dbsqlc.ReplaceAutomationParams{
			ID: string(id), UpdatedAt: encodeAutomationTimestamp(repo.now()), Revision: expectedRevision,
		}); replaceErr != nil {
			return replaceErr
		}
		if replaceErr := queries.DeleteAutomationHolds(ctx, dbsqlc.DeleteAutomationHoldsParams{
			AutomationID: string(id),
		}); replaceErr != nil {
			return replaceErr
		}
		row, replaceErr := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
		if replaceErr != nil {
			return replaceErr
		}
		record, replaceErr = automationRecord(row)
		return replaceErr
	})
	return record, err
}

// DeleteAutomation removes one current head under the expected revision. Retained
// Runs keep referencing their immutable artifacts independently of that head.
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

// checkAutomationRevision reports a current-revision mismatch as
// [automations.ErrRevisionConflict] and a missing definition as
// [automations.ErrAutomationNotFound].
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
