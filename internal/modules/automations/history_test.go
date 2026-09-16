package automations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// historyPruneBatch is the retention batch size the module must apply on its
// own, restated here so the expectation stays independent of the implementation.
const historyPruneBatch = 500

// scriptedHistoryPruner records every retention batch and replays one scripted
// deletion count per call, so tests drive the batch loop without seeding a full
// batch of rows per transaction.
type scriptedHistoryPruner struct {
	automations.AutomationRepository

	cutoffs []time.Time
	batches []int
	counts  []int64
	err     error
	onCall  func(call int)
}

var _ automations.AutomationRepository = (*scriptedHistoryPruner)(nil)

func (pruner *scriptedHistoryPruner) DeleteHistoryBefore(
	_ context.Context,
	cutoff time.Time,
	batch int,
) (int64, error) {
	call := len(pruner.batches)
	pruner.cutoffs = append(pruner.cutoffs, cutoff)
	pruner.batches = append(pruner.batches, batch)
	if pruner.onCall != nil {
		pruner.onCall(call)
	}
	if pruner.err != nil {
		return 0, pruner.err
	}
	if call >= len(pruner.counts) {
		return 0, nil
	}
	return pruner.counts[call], nil
}

func newHistoryPruneService(
	repository automations.AutomationRepository,
	dependencies automations.AutomationDependencies,
) *automations.Service {
	return automations.NewService(repository, nil, dependencies)
}

// This test protects the cutoff derived from injected retention and fails if
// PruneHistory keeps taking a caller cutoff, applies the wrong window, or stops
// normalizing the sweep time to UTC.
func TestPruneHistoryDerivesCutoffFromInjectedRetention(t *testing.T) {
	t.Parallel()
	pruner := &scriptedHistoryPruner{}
	retention := 10 * 24 * time.Hour
	service := newHistoryPruneService(pruner, automations.AutomationDependencies{
		HistoryRetention: retention,
	})

	// A non-UTC sweep time must be normalized, so a Core clock in a household
	// zone can never shift the cutoff.
	sweepTime := time.Date(
		2026, 10, 2, 12, 30, 0, 0, time.FixedZone("UTC-5", -5*60*60),
	)
	if err := service.PruneHistory(context.Background(), sweepTime); err != nil {
		t.Fatal(err)
	}

	wantCutoff := sweepTime.UTC().Add(-retention)
	if len(pruner.cutoffs) != 1 || !pruner.cutoffs[0].Equal(wantCutoff) {
		t.Fatalf("cutoffs = %v, want one cutoff at %s", pruner.cutoffs, wantCutoff)
	}
	if location := pruner.cutoffs[0].Location(); location != time.UTC {
		t.Fatalf("cutoff location = %s, want UTC", location)
	}
	if len(pruner.batches) != 1 || pruner.batches[0] != historyPruneBatch {
		t.Fatalf("batches = %v, want one batch of %d", pruner.batches, historyPruneBatch)
	}
}

// This test protects bounded batching and fails if a pass stops after one
// transaction, recomputes its cutoff per batch, or stops before a short batch
// confirms the sweep drained everything.
func TestPruneHistoryDrainsBatchesUntilShortRead(t *testing.T) {
	t.Parallel()
	pruner := &scriptedHistoryPruner{counts: []int64{historyPruneBatch, historyPruneBatch, 1}}
	retention := 10 * 24 * time.Hour
	service := newHistoryPruneService(pruner, automations.AutomationDependencies{
		HistoryRetention: retention,
	})
	sweepTime := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

	if err := service.PruneHistory(context.Background(), sweepTime); err != nil {
		t.Fatal(err)
	}
	if len(pruner.batches) != 3 {
		t.Fatalf("batches = %d, want 3", len(pruner.batches))
	}
	for call, batch := range pruner.batches {
		if batch != historyPruneBatch {
			t.Fatalf("batch %d size = %d, want %d", call, batch, historyPruneBatch)
		}
	}
	wantCutoff := sweepTime.Add(-retention)
	if len(pruner.cutoffs) != len(pruner.batches) {
		t.Fatalf("cutoffs = %d, want one per batch", len(pruner.cutoffs))
	}
	for call, cutoff := range pruner.cutoffs {
		if !cutoff.Equal(wantCutoff) {
			t.Fatalf("batch %d cutoff = %s, want %s (one cutoff per pass)", call, cutoff, wantCutoff)
		}
	}
}

// This test protects interruption of a long sweep and fails if a canceled pass
// keeps issuing batches instead of returning the cancellation.
func TestPruneHistoryStopsBatchesOnCanceledSweep(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pruner := &scriptedHistoryPruner{
		counts: []int64{historyPruneBatch, historyPruneBatch},
		onCall: func(call int) {
			if call == 0 {
				cancel()
			}
		},
	}
	service := newHistoryPruneService(pruner, automations.AutomationDependencies{
		HistoryRetention: runtimeTestHistoryRetention,
	})

	err := service.PruneHistory(ctx, runtimeTestNow)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("prune after cancel = %v, want context.Canceled", err)
	}
	if len(pruner.batches) != 1 {
		t.Fatalf("batches after cancel = %d, want 1", len(pruner.batches))
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
			retention: automations.MinimumAutomationHistoryRetention - time.Nanosecond,
			sweepTime: sweepTime,
			wantErr:   true,
		},
		{
			name:      "missing sweep time",
			retention: automations.MinimumAutomationHistoryRetention,
			wantErr:   true,
		},
		{
			name:      "minimum retention is accepted",
			retention: automations.MinimumAutomationHistoryRetention,
			sweepTime: sweepTime,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			pruner := &scriptedHistoryPruner{}
			service := newHistoryPruneService(pruner, automations.AutomationDependencies{
				HistoryRetention: test.retention,
			})

			err := service.PruneHistory(context.Background(), test.sweepTime)
			if test.wantErr {
				if err == nil {
					t.Fatal("unsafe retention pass unexpectedly succeeded")
				}
				if len(pruner.batches) != 0 {
					t.Fatalf("unsafe retention pass issued batches: %v", pruner.batches)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(pruner.batches) != 1 {
				t.Fatalf("accepted pass batches = %d, want 1", len(pruner.batches))
			}
		})
	}
}

// This test protects the strict cutoff through the real SQLite repository and
// fails if the boundary row is deleted with the older history.
func TestPruneHistoryKeepsHistoryAtTheCutoffBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, newEntityID(t)))
	if _, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID}); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	if history := listHistory(t, service, record.ID); len(history) != 1 {
		t.Fatalf("terminal history = %d rows, want 1", len(history))
	}

	// The Run recorded itself at runtimeTestNow, so one window later the cutoff
	// lands exactly on recorded_at and strict-before must retain the row.
	if err := service.PruneHistory(
		ctx, runtimeTestNow.Add(runtimeTestHistoryRetention),
	); err != nil {
		t.Fatal(err)
	}
	if history := listHistory(t, service, record.ID); len(history) != 1 {
		t.Fatalf("history at the cutoff = %d rows, want 1", len(history))
	}

	// One nanosecond past the boundary is strictly older and must be pruned.
	if err := service.PruneHistory(
		ctx, runtimeTestNow.Add(runtimeTestHistoryRetention+time.Nanosecond),
	); err != nil {
		t.Fatal(err)
	}
	if history := listHistory(t, service, record.ID); len(history) != 0 {
		t.Fatalf("history past the cutoff = %d rows, want 0", len(history))
	}
}

// This test protects the ownership boundary for failure logging and fails if a
// failed pass logs anything from inside the module, including raw error text.
func TestPruneHistoryLogsNothingOnFailedPass(t *testing.T) {
	t.Parallel()
	pruneErr := errors.New("s3cr3t-history-prune-error")
	pruner := &scriptedHistoryPruner{err: pruneErr}
	writer, logger := newAutomationLogSink()
	service := newHistoryPruneService(pruner, automations.AutomationDependencies{
		Logger:           logger,
		HistoryRetention: runtimeTestHistoryRetention,
	})

	if err := service.PruneHistory(context.Background(), runtimeTestNow); !errors.Is(err, pruneErr) {
		t.Fatalf("prune error = %v, want %v", err, pruneErr)
	}
	if records := writer.records(t); len(records) != 0 {
		t.Fatalf("failed prune logged %d records, want none:\n%s", len(records), writer.output())
	}
	if strings.Contains(writer.output(), pruneErr.Error()) {
		t.Fatalf("failed prune exposed the upstream error:\n%s", writer.output())
	}
}
