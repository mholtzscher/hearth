//go:build integration

package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestCommandReadRejectsSQLValidMalformedIdentities(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, assignment string }{
		{"command", "id = 'cmd_private_stored_value'"},
		{"correlation", "correlation_id = 'cor_private_stored_value'"},
		{"outcome observation", "status = 'satisfied', completed_at = requested_at, outcome_observation_id = 'obs_private_stored_value'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
			catalog := firstLightCatalog(t)
			repository := NewDeviceRepository(database, catalog)
			service := newTestService(repository, nil, catalog, devices.Dependencies{})
			entityID := registerHistoryEntity(t, service)
			command := newCommandRecord(t, entityID, time.Now().UTC())
			if _, err := repository.CreateCommand(t.Context(), command); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ExecContext(t.Context(), "UPDATE commands SET "+test.assignment); err != nil {
				t.Fatal(err)
			}
			id := command.ID
			if test.name == "command" {
				id = "cmd_private_stored_value"
			}
			_, err := repository.GetCommand(t.Context(), id)
			assertPrivateStoredIdentityFailure(t, err)
			_, err = repository.ListCommands(t.Context(), devices.ListCommandsParams{Limit: 10})
			assertPrivateStoredIdentityFailure(t, err)
		})
	}
}

func TestEventHistoryRejectsSQLValidMalformedIdentities(t *testing.T) {
	t.Parallel()
	for _, column := range []string{"event_id", "entity_id"} {
		t.Run(column, func(t *testing.T) {
			t.Parallel()
			database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
			now := time.Now().UTC()
			service, repository := newEntityEventTestService(t, database, &now)
			entityID := registerEntityEventEntity(t, service)
			event := newEntityEvent(t, entityID, "single_press", now)
			if _, err := service.RecordEntityEvent(
				t.Context(),
				entityEventTestAdapter,
				entityEventTestRuntimeID,
				event,
				now,
			); err != nil {
				t.Fatal(err)
			}
			prefix := "evt_"
			if column == "entity_id" {
				prefix = "ent_"
				entityID = devices.EntityID(prefix + strings.Repeat("x", 36))
			}
			if _, err := database.ExecContext(t.Context(), "UPDATE entity_events SET "+column+" = ?",
				prefix+strings.Repeat("x", 36)); err != nil {
				t.Fatal(err)
			}
			_, err := repository.ListEntityEvents(
				t.Context(),
				devices.ListEntityEventsParams{EntityID: entityID, Limit: 10},
			)
			assertPrivateStoredIdentityFailure(t, err)
		})
	}
}

func TestStateHistoryRejectsSQLValidMalformedObservationIdentity(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	entityID := registerHistoryEntity(t, service)
	// A rejected Observation has no materialized-State foreign key to update.
	observation := newObservation(t, entityID, `1`, time.Now().UTC())
	result, err := service.ProjectObservation(
		t.Context(),
		"simulator",
		testRuntimeID,
		observation,
		observation.AdapterReceivedAt,
	)
	if err != nil || result.Disposition != devices.DispositionRejected {
		t.Fatalf("seed rejected Observation: %+v, %v", result, err)
	}
	if _, err = database.ExecContext(
		t.Context(),
		"UPDATE observations SET observation_id = 'obs_private_stored_value'",
	); err != nil {
		t.Fatal(err)
	}
	_, err = repository.ListEntityStateHistory(t.Context(), devices.ListEntityStateHistoryParams{
		EntityID: entityID, Filter: devices.EntityStateHistoryFilterAll, Limit: 10,
	})
	assertPrivateStoredIdentityFailure(t, err)
}

func assertPrivateStoredIdentityFailure(t *testing.T, err error) {
	t.Helper()
	var diagnostic interface{ Cause() error }
	if err == nil || !errors.As(err, &diagnostic) || diagnostic.Cause() == nil {
		t.Fatalf("read did not reject malformed retained identity with a private cause: %v", err)
	}
	if strings.Contains(err.Error(), "private_stored_value") || strings.Contains(err.Error(), strings.Repeat("x", 36)) {
		t.Fatalf("read exposed malformed retained identity: %v", err)
	}
}
