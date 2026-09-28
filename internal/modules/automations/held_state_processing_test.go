package automations_test

import (
	"context"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type startupCutoffRepository struct {
	automations.Repository

	cutoff time.Time
}

func (repository *startupCutoffRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	snapshot devices.EntityStateSnapshot,
	now time.Time,
	startupAt time.Time,
) (automations.AdmissionResult, error) {
	repository.cutoff = startupAt
	return repository.Repository.AdmitDeviceFact(ctx, fact, snapshot, now, startupAt)
}

// A configured Core startup cutoff must remain fixed even as the admission clock advances.
func TestHeldStateStartupCutoffIsConfiguredAtConstruction(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := &startupCutoffRepository{Repository: newAutomationRepository(t, database)}
	startupAt := runtimeTestNow.Add(-time.Hour)
	current := runtimeTestNow
	service := automations.NewService(repository, newScriptedDevices(), automations.Dependencies{
		Now: func() time.Time { return current }, HeldStateStartupAt: startupAt,
	})
	entity := newEntityID(t)
	createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	current = current.Add(time.Minute)
	if _, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, current)); err != nil {
		t.Fatal(err)
	}
	if !repository.cutoff.Equal(startupAt) {
		t.Fatalf("admission startup cutoff = %s, want %s", repository.cutoff, startupAt)
	}
}
