package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// This test protects persisted eligibility through the retention entry point and
// fails if observation pruning loses the current-State anchor, applies the
// Entity Event window to observations, or retains expired observations.
func TestPruneHistoryKeepsCurrentStateAnchorAndFreshEntityEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	sweepTime := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	now := sweepTime
	service := newTestService(repository, nil, catalog, devices.Dependencies{
		Now:                  func() time.Time { return now },
		ObservationRetention: 30 * 24 * time.Hour,
	})

	eventEntityID := registerEntityEventEntity(t, service)
	binding, err := service.Register(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, validDomainRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	powerEntityID := binding.Entities[0].EntityID

	project := func(observation devices.Observation, observedAt time.Time) {
		t.Helper()
		if _, projectionErr := service.ProjectObservation(
			ctx, entityEventTestAdapter, entityEventTestRuntimeID, observation, observedAt,
		); projectionErr != nil {
			t.Fatal(projectionErr)
		}
	}
	expiredObservation := newObservation(
		t, powerEntityID, `false`, sweepTime.Add(-40*24*time.Hour),
	)
	project(expiredObservation, sweepTime.Add(-40*24*time.Hour))
	currentObservation := newObservation(t, powerEntityID, `true`, sweepTime.Add(-time.Hour))
	project(currentObservation, sweepTime.Add(-time.Hour))

	record := func(event devices.EntityEvent, receivedAt time.Time) {
		t.Helper()
		if _, recordErr := service.RecordEntityEvent(
			ctx, entityEventTestAdapter, entityEventTestRuntimeID, event, receivedAt,
		); recordErr != nil {
			t.Fatal(recordErr)
		}
	}
	// Entity Event retention compares core-owned recorded_at, which follows
	// the service clock, so the expired event is recorded under an old clock.
	now = sweepTime.Add(-40 * 24 * time.Hour)
	expiredEvent := newEntityEvent(t, eventEntityID, "single_press", now)
	record(expiredEvent, now)
	now = sweepTime
	retainedEvent := newEntityEvent(t, eventEntityID, "double_press", sweepTime.Add(-time.Hour))
	record(retainedEvent, sweepTime.Add(-time.Hour))

	if pruneErr := service.PruneHistory(ctx, sweepTime); pruneErr != nil {
		t.Fatal(pruneErr)
	}

	assertObservationIDs(t, database, []devices.ObservationID{currentObservation.ID})
	assertTableCount(t, database, "entity_states", 1)
	readStoredEntityEvent(t, database, retainedEvent.ID)
	if _, readErr := readStoredEntityEventErr(t, database, expiredEvent.ID); readErr == nil {
		t.Fatal("expired Entity Event survived the retention pass")
	}
}
