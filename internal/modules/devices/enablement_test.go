package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"testing"
	"time"
)

type enablementRepository struct {
	*stubRegistrationRepository

	params SetEntityEnabledParams
	view   EntityWithState
	err    error
	calls  int
}

func (repository *enablementRepository) SetEntityEnabled(
	_ context.Context,
	params SetEntityEnabledParams,
) (EntityWithState, error) {
	repository.calls++
	repository.params = params
	return repository.view, repository.err
}

func TestSetEntityEnabledUsesExplicitManagementAndOwnerPolicies(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.FixedZone("test", -5*60*60))
	repository := &enablementRepository{
		stubRegistrationRepository: &stubRegistrationRepository{},
		view: EntityWithState{Entity: Entity{
			ID: commandTestEntityID, AdapterID: "simulator", Enabled: false,
			Support: EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		}},
	}
	service := NewService(repository, nil, nil, Dependencies{Now: func() time.Time { return now }})

	view, err := service.SetEntityEnabled(context.Background(), commandTestEntityID, false)
	if err != nil {
		t.Fatal(err)
	}
	if repository.params.RequiredOwner != nil || repository.params.EntityID != commandTestEntityID ||
		repository.params.Enabled || !repository.params.UpdatedAt.Equal(now.UTC()) {
		t.Fatalf("management params = %#v", repository.params)
	}
	view.Entity.Support[0] = 'x'
	if repository.view.Entity.Support[0] == 'x' {
		t.Fatal("management result shares mutable support with repository")
	}

	repository.view.Entity.Enabled = true
	confirmed, err := service.SetOwnedEntityEnabled(
		context.Background(), "simulator", commandTestRuntimeID, commandTestEntityID, true,
	)
	if err != nil || !confirmed {
		t.Fatalf("owner result = %t, %v", confirmed, err)
	}
	if repository.params.RequiredOwner == nil || *repository.params.RequiredOwner != "simulator" ||
		repository.params.RequiredRuntime == nil || *repository.params.RequiredRuntime != commandTestRuntimeID {
		t.Fatalf("owner params = %#v", repository.params)
	}
}

func TestSetEntityEnabledValidatesIdentityBeforeRepositoryCall(t *testing.T) {
	t.Parallel()
	repository := &enablementRepository{stubRegistrationRepository: &stubRegistrationRepository{}}
	service := NewService(repository, nil, nil, Dependencies{})
	if _, err := service.SetEntityEnabled(context.Background(), "bad", false); err == nil {
		t.Fatal("invalid Entity ID unexpectedly accepted")
	}
	if _, err := service.SetOwnedEntityEnabled(
		context.Background(),
		"bad.adapter",
		commandTestRuntimeID,
		commandTestEntityID,
		false,
	); err == nil {
		t.Fatal("invalid Adapter ID unexpectedly accepted")
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d", repository.calls)
	}
}
