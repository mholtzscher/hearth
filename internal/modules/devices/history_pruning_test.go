package devices //nolint:testpackage // Tests exercise the package-private retention wiring.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// scriptedObservationPruner records the cutoff of each observation deletion.
type scriptedObservationPruner struct {
	cutoffs []time.Time
	err     error
}

var _ ObservationRepository = (*scriptedObservationPruner)(nil)

func (*scriptedObservationPruner) ProjectObservation(
	context.Context, ProjectObservationParams,
) (ProjectionResult, error) {
	panic("unexpected ProjectObservation call")
}

func (repository *scriptedObservationPruner) DeleteExpiredObservations(
	_ context.Context,
	before time.Time,
) error {
	repository.cutoffs = append(repository.cutoffs, before)
	return repository.err
}

// scriptedEntityEventPruner records every Entity Event batch and replays one
// scripted deletion count per call, so tests drive the batch loop without
// seeding a full batch of rows per transaction.
type scriptedEntityEventPruner struct {
	cutoffs []time.Time
	batches []int
	counts  []int64
	err     error
	onCall  func(call int)
}

var _ EntityEventRepository = (*scriptedEntityEventPruner)(nil)

func (*scriptedEntityEventPruner) RecordEntityEvent(
	context.Context, RecordEntityEventParams,
) (EntityEventRecordResult, error) {
	panic("unexpected RecordEntityEvent call")
}

func (*scriptedEntityEventPruner) ListEntityEvents(
	context.Context, ListEntityEventsParams,
) (Page[EntityEventHistoryEntry], error) {
	panic("unexpected ListEntityEvents call")
}

func (repository *scriptedEntityEventPruner) DeleteEntityEventsBefore(
	_ context.Context,
	before time.Time,
	batch int,
) (int64, error) {
	call := len(repository.batches)
	repository.cutoffs = append(repository.cutoffs, before)
	repository.batches = append(repository.batches, batch)
	if repository.onCall != nil {
		repository.onCall(call)
	}
	if repository.err != nil {
		return 0, repository.err
	}
	if call >= len(repository.counts) {
		return 0, nil
	}
	return repository.counts[call], nil
}

// newHistoryPruningService wires only the two retention stores, so retention
// tests never need the rest of the devices graph.
func newHistoryPruningService(
	observations ObservationRepository,
	entityEvents EntityEventRepository,
	retention time.Duration,
) *Service {
	return NewService(
		Stores{Observations: observations, EntityEvents: entityEvents},
		nil, nil, Dependencies{ObservationRetention: retention},
	)
}

// This test protects the single UTC sweep time and fails if PruneHistory stops
// deriving each cutoff from its own window or stops normalizing the sweep time
// to UTC.
func TestPruneHistorySweepsBothWindowsFromOneUTCTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	observations := &scriptedObservationPruner{}
	events := &scriptedEntityEventPruner{counts: []int64{0}}
	retention := 10 * 24 * time.Hour
	service := newHistoryPruningService(observations, events, retention)

	// A non-UTC sweep time must be normalized, so a Core clock in a household
	// zone can never shift the cutoff.
	sweepTime := time.Date(
		2026, 10, 2, 12, 30, 0, 0, time.FixedZone("UTC-5", -5*60*60),
	)
	if err := service.PruneHistory(ctx, sweepTime); err != nil {
		t.Fatal(err)
	}

	wantObservationCutoff := sweepTime.UTC().Add(-retention)
	if len(observations.cutoffs) != 1 || !observations.cutoffs[0].Equal(wantObservationCutoff) {
		t.Fatalf(
			"observation cutoffs = %v, want one cutoff at %s",
			observations.cutoffs, wantObservationCutoff,
		)
	}
	if location := observations.cutoffs[0].Location(); location != time.UTC {
		t.Fatalf("observation cutoff location = %s, want UTC", location)
	}

	wantEventCutoff := sweepTime.UTC().Add(-EntityEventHistoryRetention)
	if len(events.cutoffs) == 0 || !events.cutoffs[0].Equal(wantEventCutoff) {
		t.Fatalf(
			"entity event cutoffs = %v, want first cutoff at %s",
			events.cutoffs, wantEventCutoff,
		)
	}
	if location := events.cutoffs[0].Location(); location != time.UTC {
		t.Fatalf("entity event cutoff location = %s, want UTC", location)
	}
	if len(events.batches) != 1 || events.batches[0] != entityEventDeleteBatchSize {
		t.Fatalf("entity event batches = %v, want [%d]", events.batches, entityEventDeleteBatchSize)
	}
}

// This test protects failure isolation between the two deletions and fails if
// one failing deletion suppresses the other or hides its own error.
func TestPruneHistoryAttemptsBothDeletionsWhenOneFails(t *testing.T) {
	t.Parallel()
	errObservation := errors.New("observation deletion failed")
	errEntityEvent := errors.New("entity event deletion failed")
	tests := []struct {
		name           string
		observationErr error
		entityEventErr error
	}{
		{name: "observation deletion fails", observationErr: errObservation},
		{name: "entity event deletion fails", entityEventErr: errEntityEvent},
		{name: "both deletions fail", observationErr: errObservation, entityEventErr: errEntityEvent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observations := &scriptedObservationPruner{err: test.observationErr}
			events := &scriptedEntityEventPruner{counts: []int64{0}, err: test.entityEventErr}
			service := newHistoryPruningService(observations, events, 30*24*time.Hour)

			err := service.PruneHistory(
				context.Background(), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
			)
			if test.observationErr != nil && !errors.Is(err, test.observationErr) {
				t.Fatalf("prune error = %v, want %v", err, test.observationErr)
			}
			if test.entityEventErr != nil && !errors.Is(err, test.entityEventErr) {
				t.Fatalf("prune error = %v, want %v", err, test.entityEventErr)
			}
			if len(observations.cutoffs) != 1 {
				t.Fatalf("observation deletions = %d, want 1", len(observations.cutoffs))
			}
			if len(events.cutoffs) == 0 {
				t.Fatal("entity event deletion was suppressed by the other deletion's failure")
			}
		})
	}
}

// This test protects the safety floor and fails if an unconfigured, too-short,
// or timeless pass deletes anything instead of reporting the misconfiguration.
func TestPruneHistoryRejectsUnsafeRetentionAndMissingSweepTime(t *testing.T) {
	t.Parallel()
	sweepTime := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		retention time.Duration
		sweepTime time.Time
		wantErr   bool
	}{
		{name: "unconfigured retention", sweepTime: sweepTime, wantErr: true},
		{
			name:      "retention below minimum",
			retention: MinimumObservationRetention - time.Nanosecond,
			sweepTime: sweepTime,
			wantErr:   true,
		},
		{
			name:      "missing sweep time",
			retention: MinimumObservationRetention,
			wantErr:   true,
		},
		{
			name:      "minimum retention is accepted",
			retention: MinimumObservationRetention,
			sweepTime: sweepTime,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observations := &scriptedObservationPruner{}
			events := &scriptedEntityEventPruner{counts: []int64{0}}
			service := newHistoryPruningService(observations, events, test.retention)

			err := service.PruneHistory(context.Background(), test.sweepTime)
			if test.wantErr {
				if err == nil {
					t.Fatal("unsafe retention pass unexpectedly succeeded")
				}
				if len(observations.cutoffs) != 0 || len(events.cutoffs) != 0 {
					t.Fatalf(
						"unsafe retention pass deleted: observations = %v, entity events = %v",
						observations.cutoffs, events.cutoffs,
					)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(observations.cutoffs) != 1 || len(events.cutoffs) != 1 {
				t.Fatalf(
					"accepted pass deletions: observations = %d, entity events = %d, want 1 each",
					len(observations.cutoffs), len(events.cutoffs),
				)
			}
		})
	}
}

// This test protects bounded Entity Event batching and fails if a pass stops
// after one transaction, recomputes its cutoff per batch, or stops before a
// short batch confirms the sweep drained everything.
func TestPruneHistoryDrainsEntityEventsInBoundedBatches(t *testing.T) {
	t.Parallel()
	observations := &scriptedObservationPruner{}
	events := &scriptedEntityEventPruner{counts: []int64{
		entityEventDeleteBatchSize,
		entityEventDeleteBatchSize,
		1,
	}}
	service := newHistoryPruningService(observations, events, 30*24*time.Hour)

	sweepTime := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if err := service.PruneHistory(context.Background(), sweepTime); err != nil {
		t.Fatal(err)
	}
	if len(events.batches) != 3 {
		t.Fatalf("entity event batches = %d, want 3", len(events.batches))
	}
	for call, batch := range events.batches {
		if batch != entityEventDeleteBatchSize {
			t.Fatalf("batch %d size = %d, want %d", call, batch, entityEventDeleteBatchSize)
		}
	}
	wantCutoff := sweepTime.Add(-EntityEventHistoryRetention)
	if len(events.cutoffs) != len(events.batches) {
		t.Fatalf("entity event cutoffs = %d, want one per batch", len(events.cutoffs))
	}
	for call, cutoff := range events.cutoffs {
		if !cutoff.Equal(wantCutoff) {
			t.Fatalf("batch %d cutoff = %s, want %s", call, cutoff, wantCutoff)
		}
	}
}

// This test protects interruption of a long sweep and fails if a canceled pass
// keeps issuing batches instead of returning the cancellation.
func TestPruneHistoryStopsEntityEventBatchesOnCanceledSweep(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observations := &scriptedObservationPruner{}
	events := &scriptedEntityEventPruner{
		counts: []int64{
			entityEventDeleteBatchSize,
			entityEventDeleteBatchSize,
		},
		onCall: func(call int) {
			if call == 0 {
				cancel()
			}
		},
	}
	service := newHistoryPruningService(observations, events, 30*24*time.Hour)

	err := service.PruneHistory(ctx, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("prune after cancel = %v, want context.Canceled", err)
	}
	if len(events.batches) != 1 {
		t.Fatalf("entity event batches after cancel = %d, want 1", len(events.batches))
	}
}

// This test protects the ownership boundary for failure logging and fails if a
// failed devices pass logs anything from inside the module, including raw error
// text. The app logs one structured event per failed module pass instead.
func TestPruneHistoryLogsNothingOnFailedPass(t *testing.T) {
	t.Parallel()
	writer, _, logger := newCommandLogSink()
	observations := &scriptedObservationPruner{err: errors.New("s3cr3t-observation-prune-error")}
	events := &scriptedEntityEventPruner{
		err: errors.New("s3cr3t-entity-event-prune-error"),
	}
	service := NewService(
		Stores{Observations: observations, EntityEvents: events},
		nil, nil, Dependencies{Logger: logger, ObservationRetention: 30 * 24 * time.Hour},
	)

	err := service.PruneHistory(
		context.Background(), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
	)
	if err == nil {
		t.Fatal("failed devices pass unexpectedly succeeded")
	}
	if records := writer.records(t); len(records) != 0 {
		t.Fatalf("failed pass logged %d records, want none:\n%s", len(records), writer.output())
	}
}
