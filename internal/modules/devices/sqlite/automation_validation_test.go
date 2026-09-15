package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationValidationFixture registers one stateful commandable Entity and one
// stateless Entity Event source, which is exactly the kind split the automation
// Trigger validation must distinguish.
func automationValidationFixture(t *testing.T) (*devices.Service, devices.EntityID, devices.EntityID) {
	t.Helper()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	service, _ := newEntityEventTestService(t, database, &now)
	ctx := context.Background()
	light, err := service.Register(
		ctx, entityEventTestAdapter, entityEventTestRuntimeID, validDomainRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	eventSourceID := registerEntityEventEntity(t, service)
	return service, light.Entities[0].EntityID, eventSourceID
}

// TestValidateObservationTriggerProtectsEntityKindAndStatefulness protects the
// Observation Trigger source rule: only a currently existing stateful Entity is
// accepted, so an Entity Event source and a missing Entity are rejected while
// the permanent classification stays distinguishable.
func TestValidateObservationTriggerProtectsEntityKindAndStatefulness(t *testing.T) {
	t.Parallel()
	service, statefulID, eventSourceID := automationValidationFixture(t)
	ctx := context.Background()

	if err := service.ValidateObservationTrigger(ctx, statefulID); err != nil {
		t.Fatalf("stateful entity rejected: %v", err)
	}
	if err := service.ValidateObservationTrigger(ctx, eventSourceID); !errors.Is(
		err, devices.ErrAutomationTriggerSource,
	) {
		t.Fatalf("stateless entity error = %v, want ErrAutomationTriggerSource", err)
	}
	missingID := newTestEntityID(t)
	if err := service.ValidateObservationTrigger(ctx, missingID); !errors.Is(err, devices.ErrEntityNotFound) {
		t.Fatalf("missing entity error = %v, want ErrEntityNotFound", err)
	}
	if err := service.ValidateObservationTrigger(ctx, devices.EntityID("ent_not-a-uuid")); !errors.Is(
		err, devices.ErrAutomationTriggerSource,
	) {
		t.Fatalf("malformed entity error = %v, want ErrAutomationTriggerSource", err)
	}
}

// TestValidateObservationTriggerAllowsDisabledEntity protects that save-time
// validation proves current references only: an Entity that is currently
// disabled still validates, because control eligibility belongs to the normal
// Command execution-time path.
func TestValidateObservationTriggerAllowsDisabledEntity(t *testing.T) {
	t.Parallel()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	service, _ := newEntityEventTestService(t, database, &now)
	disabled := false
	registration := validDomainRegistration()
	registration.Entities[0].InitiallyEnabled = &disabled
	binding, err := service.Register(
		context.Background(), entityEventTestAdapter, entityEventTestRuntimeID, registration,
	)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Entities[0].Enabled {
		t.Fatal("fixture entity is enabled, want disabled")
	}
	if err = service.ValidateObservationTrigger(context.Background(), binding.Entities[0].EntityID); err != nil {
		t.Fatalf("disabled entity rejected: %v", err)
	}
}

// TestValidateEntityEventTriggerRequiresExactSupportedName protects the Entity
// Event Trigger source rule: the Entity must currently be an Entity Event source
// supporting the exact name, so a wrong-kind Entity and an unsupported or
// malformed name are rejected.
func TestValidateEntityEventTriggerRequiresExactSupportedName(t *testing.T) {
	t.Parallel()
	service, statefulID, eventSourceID := automationValidationFixture(t)
	ctx := context.Background()

	if err := service.ValidateEntityEventTrigger(ctx, eventSourceID, "single_press"); err != nil {
		t.Fatalf("supported event rejected: %v", err)
	}
	if err := service.ValidateEntityEventTrigger(
		ctx, eventSourceID, "long_press",
	); !errors.Is(err, devices.ErrAutomationTriggerSource) {
		t.Fatalf("unsupported event error = %v, want ErrAutomationTriggerSource", err)
	}
	if err := service.ValidateEntityEventTrigger(
		ctx, eventSourceID, "Not A Slug",
	); !errors.Is(err, devices.ErrAutomationTriggerSource) {
		t.Fatalf("malformed event error = %v, want ErrAutomationTriggerSource", err)
	}
	if err := service.ValidateEntityEventTrigger(
		ctx, statefulID, "single_press",
	); !errors.Is(err, devices.ErrAutomationTriggerSource) {
		t.Fatalf("stateful entity error = %v, want ErrAutomationTriggerSource", err)
	}
	missingID := newTestEntityID(t)
	if err := service.ValidateEntityEventTrigger(
		ctx, missingID, "single_press",
	); !errors.Is(err, devices.ErrEntityNotFound) {
		t.Fatalf("missing entity error = %v, want ErrEntityNotFound", err)
	}
}

// TestValidateCommandStaysTheExecutionEligibilityFreeSeam protects that the
// devices seam exposes normalized parameters without requiring enablement or
// availability, which is what the automation definition validator consumes.
func TestValidateCommandStaysTheExecutionEligibilityFreeSeam(t *testing.T) {
	t.Parallel()
	service, statefulID, _ := automationValidationFixture(t)
	parameters, err := service.ValidateCommand(context.Background(), devices.CommandInput{
		EntityID:      statefulID,
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(parameters) != `{"value":true}` {
		t.Fatalf("normalized parameters = %s", parameters)
	}
}
