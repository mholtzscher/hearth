//go:build integration

package sqlite //nolint:testpackage // Test the SQLite repository and projection together.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func TestForecastMigrationAllowsSnapshotAndNextObservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := dbtest.OpenAtVersion(t, filepath.Join(t.TempDir(), "forecast.db"), 31)
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	if err := adapterRepository(
		repository,
	).ClaimAdapterRuntime(ctx, testAdapterClaim("simulator", claimedAt)); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	registration := validDomainRegistration()
	registration.Entities[0].TypeID = devices.EntityTypeID("hearth.hourlyforecast/v1")
	registration.Entities[0].Support = devices.EntitySupport(`{"state":{},"operations":{}}`)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	entity := binding.Entities[0].EntityID
	at := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	const neutral = `{"periods":[{"starts_at":"2026-11-10T00:00:00Z","temperature":12.3}]}`
	const legacy = `{"periods":[{"starts_at":"2026-11-10T00:00:00Z","temperature_c":12.3}]}`
	old := newObservation(t, entity, neutral, at)
	current := newObservation(t, entity, neutral, at.Add(time.Second))
	for _, observation := range []devices.Observation{old, current} {
		if _, err = service.ProjectObservation(ctx, "simulator", testRuntimeID, observation, at); err != nil {
			t.Fatal(err)
		}
	}
	// The older fact has already been delivered, so its Observation is audit-only.
	if _, err = database.Exec(`DELETE FROM device_facts_outbox WHERE source_id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(
		`UPDATE observations SET state_value_json=? WHERE entity_id=?`,
		legacy,
		entity,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`UPDATE entity_states SET value_json=? WHERE entity_id=?`, legacy, entity); err != nil {
		t.Fatal(err)
	}
	provider, err := platformdb.NewMigrationProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 32); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.GetEntityStateSnapshot(ctx, []devices.EntityID{entity})
	if err != nil {
		t.Fatal(err)
	}
	if state := snapshot.Entries[entity].State; state == nil || state.ObservationID != current.ID {
		t.Fatalf("migrated snapshot = %#v", state)
	}
	var history string
	if err = database.QueryRow(`SELECT state_value_json FROM observations WHERE observation_id=?`, old.ID).
		Scan(&history); err != nil ||
		history != legacy {
		t.Fatalf("historical value = %q, %v", history, err)
	}
	next := newObservation(t, entity, neutral, at.Add(2*time.Second))
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, next, at.Add(2*time.Second))
	if err != nil || result.Disposition != devices.DispositionUnchanged {
		t.Fatalf("next observation = %#v, %v; want unchanged", result, err)
	}
}
