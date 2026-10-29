//go:build integration

package sqlite //nolint:testpackage // Tests inspect stored evidence after repository writes.

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestObservationTimestampsMatchStateAndFactsWithoutLosingPrecision(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	binding, err := service.Register(t.Context(), "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	var ids []devices.ObservationID
	for _, nanos := range []time.Duration{0, 100 * time.Millisecond, 123456789 * time.Nanosecond} {
		at := base.Add(nanos)
		observation := newObservation(t, binding.Entities[0].EntityID, `true`, at)
		if nanos != 0 {
			observation.SourceUpdatedAt = &at
		}
		if _, err = service.ProjectObservation(t.Context(), "simulator", testRuntimeID, observation, at); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, observation.ID)
	}
	// These literal values are independent of the production formatter. The
	// duplicate source instant must have identical TEXT bytes in all three rows.
	var observationAt, stateAt, factAt string
	if err = database.QueryRowContext(t.Context(), `SELECT o.source_updated_at, s.source_updated_at, f.source_updated_at
		FROM observations o JOIN entity_states s ON s.observation_id = o.observation_id
		JOIN device_facts_outbox f ON f.source_id = o.observation_id
		WHERE o.observation_id = ?`, ids[2]).Scan(&observationAt, &stateAt, &factAt); err != nil {
		t.Fatal(err)
	}
	const precise = "2026-10-28T12:00:00.123456789Z"
	if observationAt != precise || stateAt != precise || factAt != precise {
		t.Fatalf("stored source evidence = %q, %q, %q; want %q", observationAt, stateAt, factAt, precise)
	}
	var receivedAt, observedAt string
	var absent sql.NullString
	if err = database.QueryRowContext(t.Context(), `SELECT adapter_received_at, observed_at, source_updated_at
		FROM observations WHERE observation_id = ?`, ids[0]).Scan(&receivedAt, &observedAt, &absent); err != nil {
		t.Fatal(err)
	}
	if receivedAt != "2026-10-28T12:00:00.000000000Z" || receivedAt != observedAt || absent.Valid {
		t.Fatalf("whole-second evidence = %q, %q, %#v", receivedAt, observedAt, absent)
	}
	var matches int
	if err = database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM observations
		WHERE adapter_received_at = observed_at AND source_updated_at = ?`,
		"2026-10-28T12:00:00.100000000Z").Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if matches != 1 {
		t.Fatalf("canonical fractional timestamp equality matched %d observations, want 1", matches)
	}
	if err = repository.DeleteExpiredObservations(t.Context(), base.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	assertObservationIDs(t, database, ids[1:])
	// Pruning retained observations must not discard their pending facts.
	facts, err := repository.ListPendingDeviceFacts(t.Context(), 10)
	if err != nil || len(facts) != 3 {
		t.Fatalf("pending facts after retention = %d, error = %v", len(facts), err)
	}
}

func TestCommandDiagnosticTimestampsPreserveBackwardClockCorrection(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	binding, err := service.Register(t.Context(), "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	requested := time.Date(2026, 10, 28, 12, 0, 0, 100000000, time.UTC)
	claimTestAdapterRuntime(t, repository, requested)
	command := newCommandRecord(t, binding.Entities[0].EntityID, requested)
	if _, err = repository.CreateCommand(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if err = repository.MarkCommandAccepted(t.Context(), command.ID, requested); err != nil {
		t.Fatal(err)
	}
	completed := requested.Add(-100 * time.Millisecond)
	if err = repository.CompleteCommand(t.Context(), devices.CommandCompletion{
		ID: command.ID, Status: devices.CommandStatusRejected, CompletedAt: completed,
		FailureCode: devices.CommandFailureUpstreamRejected,
	}); err != nil {
		t.Fatal(err)
	}
	var acceptedAt, completedAt string
	if err = database.QueryRowContext(t.Context(), `SELECT accepted_at, completed_at FROM commands WHERE id = ?`,
		command.ID).Scan(&acceptedAt, &completedAt); err != nil {
		t.Fatal(err)
	}
	if acceptedAt != "2026-10-28T12:00:00.100000000Z" || completedAt != "2026-10-28T12:00:00.000000000Z" {
		t.Fatalf("command diagnostic times = %q, %q", acceptedAt, completedAt)
	}
	stored, err := repository.GetCommand(t.Context(), command.ID)
	if err != nil || stored.CompletedAt == nil || !stored.CompletedAt.Equal(completed) {
		t.Fatalf("backward-clock completion = %#v, error = %v", stored, err)
	}
}
