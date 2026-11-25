package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"testing"
	"time"
)

type enablementRepository struct {
	params SetEntityEnabledParams
	view   EntityWithState
	calls  int
}

func (repository *enablementRepository) SetEntityEnabled(
	_ context.Context,
	params SetEntityEnabledParams,
) (EntityWithState, error) {
	repository.calls++
	repository.params = params
	return repository.view, nil
}

func TestSetOwnedEntityEnabledFencesToOwnerRuntime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.FixedZone("test", -5*60*60))
	repository := &enablementRepository{
		view: EntityWithState{Entity: Entity{ID: commandTestEntityID, AdapterID: "simulator", Enabled: true}},
	}
	service := newTestService(repository, nil, nil, Dependencies{Now: func() time.Time { return now }})

	confirmed, err := service.SetOwnedEntityEnabled(
		context.Background(), "simulator", commandTestRuntimeID, commandTestEntityID, true,
	)
	if err != nil || !confirmed {
		t.Fatalf("owner result = %t, %v", confirmed, err)
	}
	if repository.params.EntityID != commandTestEntityID || !repository.params.Enabled ||
		!repository.params.UpdatedAt.Equal(now.UTC()) ||
		repository.params.RequiredOwner == nil || *repository.params.RequiredOwner != "simulator" ||
		repository.params.RequiredRuntime == nil || *repository.params.RequiredRuntime != commandTestRuntimeID {
		t.Fatalf("owner params = %#v", repository.params)
	}
}

func TestSetOwnedEntityEnabledValidatesIdentityBeforeRepositoryCall(t *testing.T) {
	t.Parallel()
	repository := &enablementRepository{}
	service := newTestService(repository, nil, nil, Dependencies{})
	for _, test := range []struct {
		name      string
		adapterID string
		entityID  EntityID
	}{
		{"adapter", "bad.adapter", commandTestEntityID},
		{"entity", "simulator", "bad"},
	} {
		if _, err := service.SetOwnedEntityEnabled(
			context.Background(), test.adapterID, commandTestRuntimeID, test.entityID, false,
		); err == nil {
			t.Fatalf("invalid %s ID unexpectedly accepted", test.name)
		}
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d", repository.calls)
	}
}
