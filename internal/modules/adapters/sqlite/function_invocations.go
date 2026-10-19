package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

func (repository *AdapterRepository) Admit(
	ctx context.Context,
	prepared adapters.PreparedInvocation,
) (adapters.Invocation, error) {
	if !prepared.Ready() {
		return adapters.Invocation{}, adapters.ErrInvalidFunctionInput
	}
	ctx, cancel := context.WithDeadline(ctx, prepared.Deadline())
	defer cancel()
	function := prepared.Function()
	descriptor, _ := json.Marshal(function.Descriptor)
	var invocation adapters.Invocation
	err := repository.functionTransaction(ctx, func(queries *dbsqlc.Queries) error {
		row, err := queries.GetFunctionCatalog(ctx, dbsqlc.GetFunctionCatalogParams{AdapterID: function.AdapterID})
		if errors.Is(err, sql.ErrNoRows) {
			return adapters.ErrCatalogChanged
		}
		if err != nil {
			return err
		}
		if row.CatalogRevision != string(function.CatalogRevision) {
			return adapters.ErrCatalogChanged
		}
		if err = matchStoredFunctionDescriptor(row.FunctionsJson, function.Descriptor.Key, descriptor); err != nil {
			return err
		}
		_, err = queries.FunctionRuntimeActive(
			ctx,
			dbsqlc.FunctionRuntimeActiveParams{AdapterID: function.AdapterID, RuntimeID: row.RuntimeID},
		)
		if errors.Is(err, sql.ErrNoRows) {
			return adapters.ErrAdapterUnavailable
		}
		if err != nil {
			return err
		}
		id, err := adapters.NewInvocationID()
		if err != nil {
			return err
		}
		invocation = adapters.Invocation{
			ID:              id,
			CatalogRevision: function.CatalogRevision,
			Function:        function.Descriptor,
			Route: adapters.RouteSnapshot{
				AdapterID: function.AdapterID,
				RuntimeID: adapters.RuntimeID(row.RuntimeID),
			},
			Arguments: prepared.Arguments(),
			StartedAt: prepared.StartedAt(),
			Deadline:  prepared.Deadline(),
		}
		return queries.InsertFunctionInvocation(
			ctx,
			dbsqlc.InsertFunctionInvocationParams{
				ID:              string(id),
				AdapterID:       function.AdapterID,
				RuntimeID:       row.RuntimeID,
				CatalogRevision: row.CatalogRevision,
				FunctionKey:     string(function.Descriptor.Key),
				DescriptorJson: string(
					descriptor,
				),
				ArgumentsJson: string(invocation.Arguments),
				StartedAt:     functionTimestamp(invocation.StartedAt),
				Deadline:      functionTimestamp(invocation.Deadline),
			},
		)
	})
	if err != nil {
		return adapters.Invocation{}, err
	}
	return invocation, nil
}

func matchStoredFunctionDescriptor(raw string, key adapters.FunctionKey, prepared []byte) error {
	var descriptors []adapters.Descriptor
	if decodeFunctionJSON(raw, &descriptors) != nil {
		return adapters.ErrFunctionPersistence
	}
	for _, stored := range descriptors {
		if stored.Key != key {
			continue
		}
		encoded, err := json.Marshal(stored)
		if err != nil {
			return adapters.ErrFunctionPersistence
		}
		if bytes.Equal(encoded, prepared) {
			return nil
		}
		return adapters.ErrCatalogChanged
	}
	return adapters.ErrCatalogChanged
}

func (repository *AdapterRepository) Complete(
	ctx context.Context,
	id adapters.InvocationID,
	evidence adapters.Evidence,
	at time.Time,
) (adapters.Invocation, error) {
	if _, err := adapters.ParseInvocationID(
		string(id),
	); err != nil || !adapters.ValidFunctionTime(at) ||
		evidence == nil {
		return adapters.Invocation{}, adapters.ErrInvalidFunctionInput
	}
	var invocation adapters.Invocation
	err := repository.functionTransaction(ctx, func(queries *dbsqlc.Queries) error {
		row, err := queries.GetFunctionInvocation(ctx, dbsqlc.GetFunctionInvocationParams{ID: string(id)})
		if errors.Is(err, sql.ErrNoRows) {
			return adapters.ErrInvocationNotFound
		}
		if err != nil {
			return err
		}
		retained, output, err := mapFunctionInvocation(row)
		if err != nil {
			return err
		}
		normalized, err := adapters.PrepareFunctionEvidence(evidence, output, retained.StartedAt, retained.Deadline, at)
		if err != nil {
			return err
		}
		if retained.Completion != nil {
			invocation = retained
			return nil
		}
		status, result, reason := functionEvidenceColumns(normalized)
		_, err = queries.CompleteFunctionInvocation(
			ctx,
			dbsqlc.CompleteFunctionInvocationParams{ID: string(id), Status: status,
				ResultJson: result, ReasonCode: reason, CompletedAt: nullableText(functionTimestamp(at))},
		)
		if err != nil {
			return err
		}
		retained.Completion = &adapters.Completion{At: at, Evidence: normalized}
		invocation = retained
		return nil
	})
	return invocation, err
}

func (repository *AdapterRepository) GetInvocation(
	ctx context.Context,
	id adapters.InvocationID,
) (adapters.Invocation, error) {
	if _, err := adapters.ParseInvocationID(string(id)); err != nil {
		return adapters.Invocation{}, err
	}
	row, err := repository.queries.GetFunctionInvocation(ctx, dbsqlc.GetFunctionInvocationParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return adapters.Invocation{}, adapters.ErrInvocationNotFound
	}
	if err != nil {
		return adapters.Invocation{}, err
	}
	invocation, _, err := mapFunctionInvocation(row)
	return invocation, err
}

func (repository *AdapterRepository) ListInvocations(
	ctx context.Context,
	filter adapters.InvocationFilter,
) (adapters.InvocationPage, error) {
	if adapters.ValidateInvocationFilter(filter) != nil {
		return adapters.InvocationPage{}, adapters.ErrInvalidFunctionInput
	}
	before, id, err := adapters.DecodeInvocationCursor(filter)
	if err != nil {
		return adapters.InvocationPage{}, err
	}
	limit, _ := adapters.FunctionPageLimit(filter.Limit)
	position := ""
	if !before.IsZero() {
		position = functionTimestamp(before)
	}
	result := adapters.InvocationPage{Items: make([]adapters.Invocation, 0)}
	err = repository.functionTransaction(ctx, func(queries *dbsqlc.Queries) error {
		if readErr := functionAdapterExists(ctx, queries, filter.AdapterID); readErr != nil {
			return readErr
		}
		rows, readErr := queries.ListFunctionInvocations(
			ctx,
			dbsqlc.ListFunctionInvocationsParams{
				AdapterID: filter.AdapterID,
				FunctionKey: string(
					filter.FunctionKey,
				),
				BeforeTime: position,
				BeforeID:   string(id),
				PageLimit:  int64(limit + 1),
			},
		)
		if readErr != nil {
			return readErr
		}
		for index, row := range rows {
			if index == limit {
				last := result.Items[index-1]
				result.NextCursor = adapters.EncodeInvocationCursor(filter, last.StartedAt, last.ID)
				break
			}
			invocation, _, decodeErr := mapFunctionInvocation(row)
			if decodeErr != nil {
				return decodeErr
			}
			result.Items = append(result.Items, invocation)
		}
		return nil
	})
	return result, err
}

func (repository *AdapterRepository) Recover(ctx context.Context, at time.Time) error {
	if !adapters.ValidFunctionTime(at) {
		return adapters.ErrInvalidFunctionInput
	}
	return repository.functionTransaction(ctx, func(queries *dbsqlc.Queries) error {
		latest, err := queries.LatestRunningFunctionStart(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		start, err := time.Parse(time.RFC3339Nano, latest)
		if err != nil || !adapters.ValidFunctionTime(start) {
			return adapters.ErrFunctionPersistence
		}
		if at.Before(start) {
			return adapters.ErrInvalidFunctionInput
		}
		return queries.RecoverFunctionInvocations(
			ctx,
			dbsqlc.RecoverFunctionInvocationsParams{CompletedAt: nullableText(functionTimestamp(at))},
		)
	})
}

func (repository *AdapterRepository) DeleteHistoryBefore(ctx context.Context, at time.Time, limit int) (int64, error) {
	if !adapters.ValidFunctionTime(at) || limit < 1 || limit > adapters.FunctionHistoryBatchSize {
		return 0, adapters.ErrInvalidFunctionInput
	}
	return repository.queries.DeleteFunctionHistoryBefore(
		ctx,
		dbsqlc.DeleteFunctionHistoryBeforeParams{CompletedAt: nullableText(functionTimestamp(at)), Limit: int64(limit)},
	)
}

func functionEvidenceColumns(evidence adapters.Evidence) (string, sql.NullString, sql.NullString) {
	switch outcome := evidence.(type) {
	case adapters.Succeeded:
		return "succeeded", nullableText(string(outcome.Result)), sql.NullString{}
	case adapters.Failed:
		return "failed", sql.NullString{}, nullableText(outcome.ReasonCode)
	case adapters.NotExecuted:
		return "not_executed", sql.NullString{}, nullableText(outcome.ReasonCode)
	case adapters.Unknown:
		return "unknown", sql.NullString{}, nullableText(outcome.ReasonCode)
	default:
		return "", sql.NullString{}, sql.NullString{}
	}
}
