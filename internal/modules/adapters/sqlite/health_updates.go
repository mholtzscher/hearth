package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

// appendAdapterHealthTransition owns only Adapter-kind history. The immutable
// head and executable publication evidence commit in the caller's transaction.
func appendAdapterHealthTransition(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params dbsqlc.InsertHealthTransitionParams,
) error {
	revision := adapters.HealthRevision(1)
	head, err := queries.GetHealthHead(ctx, dbsqlc.GetHealthHeadParams{AdapterID: params.AdapterID})
	if err == nil {
		if _, _, err = retainedSnapshot(head.AdapterID, head.Revision, head.SnapshotJson); err != nil {
			return err
		}
		if err = adapters.ValidateHealthTraceMetadata(head.Traceparent, head.Tracestate); err != nil {
			return fmt.Errorf("%w: invalid head trace", adapters.ErrCorruptHealthEvidence)
		}
		if head.Revision == math.MaxInt64 {
			return adapters.ErrHealthRevisionOverflow
		}
		revision = adapters.HealthRevision(head.Revision + 1)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("get health head: %w", err)
	}
	observed, err := parseTime(params.ObservedAt)
	if err != nil {
		return err
	}
	sourceObserved, err := parseOptionalTime(params.SourceObservedAt)
	if err != nil {
		return err
	}
	snapshot := adapters.HealthSnapshot{
		AdapterID: params.AdapterID,
		Revision:  revision,
		Status:    adapters.AdapterHealthStatus(params.Status),
		Source:    params.Source,
		Reason: healthReasonFromNull(
			params.ReasonCode,
		),
		ChangedAt:        observed,
		EvidenceAt:       observed,
		SourceObservedAt: sourceObserved,
	}
	if params.RuntimeID.Valid {
		runtime := adapters.RuntimeID(params.RuntimeID.String)
		snapshot.RuntimeID = &runtime
	}
	body, err := adapters.EncodeHealthSnapshot(snapshot)
	if err != nil {
		return err
	}
	metadata, err := adapters.HealthPublicationMetadata(ctx)
	if err != nil {
		return err
	}
	if _, err = queries.InsertHealthTransition(ctx, params); err != nil {
		return fmt.Errorf("insert Adapter health transition: %w", err)
	}
	if err = persistHead(ctx, queries, snapshot, body, metadata); err != nil {
		return err
	}
	return enqueueSnapshot(ctx, queries, snapshot, body, observed, metadata)
}

func persistHead(
	ctx context.Context,
	queries *dbsqlc.Queries,
	snapshot adapters.HealthSnapshot,
	body []byte,
	metadata adapters.PendingHealthUpdate,
) error {
	if err := queries.UpsertHealthHead(ctx, dbsqlc.UpsertHealthHeadParams{
		AdapterID: snapshot.AdapterID,
		Revision: int64(
			snapshot.Revision,
		),
		SnapshotJson: string(body),
		Traceparent:  metadata.TraceParent,
		Tracestate:   metadata.TraceState,
	}); err != nil {
		return fmt.Errorf("persist health head: %w", err)
	}
	return nil
}

func enqueueSnapshot(
	ctx context.Context,
	queries *dbsqlc.Queries,
	snapshot adapters.HealthSnapshot,
	body []byte,
	enqueuedAt time.Time,
	metadata adapters.PendingHealthUpdate,
) error {
	id, err := adapters.NewHealthUpdateID()
	if err != nil {
		return err
	}
	metadata.ID = id
	metadata.EnqueuedAt = enqueuedAt
	if err = adapters.ValidateHealthPublicationMetadata(metadata); err != nil {
		return err
	}
	var causation sql.NullString
	if metadata.CausationID != nil {
		causation = nullableText(*metadata.CausationID)
	}
	if err = queries.InsertHealthUpdate(ctx, dbsqlc.InsertHealthUpdateParams{
		MessageID: string(
			id,
		),
		AdapterID:     snapshot.AdapterID,
		Revision:      int64(snapshot.Revision),
		SnapshotJson:  string(body),
		EnqueuedAt:    formatTime(enqueuedAt),
		CorrelationID: metadata.CorrelationID,
		CausationID:   causation,
		Traceparent:   metadata.TraceParent,
		Tracestate:    metadata.TraceState,
	}); err != nil {
		return fmt.Errorf("enqueue Adapter health: %w", err)
	}
	return nil
}

func (repository *AdapterRepository) QueueCurrentHealthSnapshots(ctx context.Context, enqueuedAt time.Time) error {
	if err := adapters.ValidateUTCTime(enqueuedAt); err != nil {
		return err
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin health snapshot synchronization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	instances, err := queries.ListAdapterInstances(ctx)
	if err != nil {
		return fmt.Errorf("list Adapter health snapshots: %w", err)
	}
	for _, instance := range instances {
		if err = queueCurrentHealthSnapshot(ctx, queries, instance, enqueuedAt); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit health snapshot synchronization: %w", err)
	}
	return nil
}

func queueCurrentHealthSnapshot(
	ctx context.Context,
	queries *dbsqlc.Queries,
	instance dbsqlc.AdapterInstance,
	enqueuedAt time.Time,
) error {
	metadata, err := adapters.HealthPublicationMetadata(ctx)
	if err != nil {
		return err
	}
	head, err := queries.GetHealthHead(ctx, dbsqlc.GetHealthHeadParams{AdapterID: instance.AdapterID})
	var snapshot adapters.HealthSnapshot
	var body []byte
	switch {
	case err == nil:
		snapshot, body, err = retainedSnapshot(head.AdapterID, head.Revision, head.SnapshotJson)
		if err != nil {
			return err
		}
		// Reuse canonical retained evidence, never mutable heartbeat values.
		metadata.TraceParent = head.Traceparent
		metadata.TraceState = head.Tracestate
		if err = adapters.ValidateHealthTraceMetadata(head.Traceparent, head.Tracestate); err != nil {
			return fmt.Errorf("%w: invalid head trace", adapters.ErrCorruptHealthEvidence)
		}
	case errors.Is(err, sql.ErrNoRows):
		snapshot, err = healthSnapshotFromRow(instance)
		if err != nil {
			return fmt.Errorf("%w: invalid legacy Adapter health", adapters.ErrCorruptHealthEvidence)
		}
		body, err = adapters.EncodeHealthSnapshot(snapshot)
		if err != nil {
			return fmt.Errorf("%w: invalid legacy Adapter health", adapters.ErrCorruptHealthEvidence)
		}
		if err = persistHead(ctx, queries, snapshot, body, metadata); err != nil {
			return err
		}
	default:
		return fmt.Errorf("get synchronization health head: %w", err)
	}
	if err = enqueueSnapshot(ctx, queries, snapshot, body, enqueuedAt, metadata); err != nil {
		return err
	}
	return nil
}

func (repository *AdapterRepository) ListPendingHealthUpdates(
	ctx context.Context, limit int,
) ([]adapters.PendingHealthUpdate, error) {
	if limit < 1 {
		return nil, errors.New("health publication limit must be positive")
	}
	rows, err := repository.queries.ListPendingHealthUpdates(
		ctx,
		dbsqlc.ListPendingHealthUpdatesParams{Limit: int64(limit)},
	)
	if err != nil {
		return nil, fmt.Errorf("read pending Adapter health: %w", err)
	}
	pending := make([]adapters.PendingHealthUpdate, 0, len(rows))
	for _, row := range rows {
		update, decodeErr := pendingHealthUpdateFromRow(row)
		if decodeErr != nil {
			return pending, decodeErr
		}
		pending = append(pending, update)
	}
	return pending, nil
}

func (repository *AdapterRepository) DeleteHealthUpdate(ctx context.Context, id adapters.HealthUpdateID) error {
	if _, err := adapters.ParseHealthUpdateID(string(id)); err != nil {
		return err
	}
	if err := repository.queries.DeleteHealthUpdate(
		ctx,
		dbsqlc.DeleteHealthUpdateParams{MessageID: string(id)},
	); err != nil {
		return fmt.Errorf("delete Adapter health update: %w", err)
	}
	return nil
}
