package automations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// definitionTestRepository promotes the real SQLite definition adapter and
// declares the runtime persistence methods the full seam carries. D1 service
// tests never call those methods, so an explicit error keeps a stray call from
// silently passing.
type definitionTestRepository struct {
	*automations.SQLiteRepository
}

var _ automations.AutomationRepository = (*definitionTestRepository)(nil)

// The concrete devices service must satisfy the consumer-defined seam the
// automation service validates against; a signature drift here would otherwise
// surface only at application wiring time.
var _ automations.AutomationDevices = (*devices.Service)(nil)

func (*definitionTestRepository) AdmitDeviceFact(
	context.Context, automations.DeviceFact, time.Time,
) (automations.AdmissionResult, error) {
	return automations.AdmissionResult{}, errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) AdmitManualRun(
	context.Context, automations.AutomationID, time.Time,
) (automations.AutomationRun, error) {
	return automations.AutomationRun{}, errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) MarkStepRunning(context.Context, automations.StepStart) error {
	return errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) CompleteStep(context.Context, automations.StepCompletion) error {
	return errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) CompleteRun(context.Context, automations.RunCompletion) error {
	return errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) GetHistoryEntry(
	context.Context, automations.AutomationID, string,
) (automations.AutomationHistoryEntry, error) {
	return automations.AutomationHistoryEntry{}, errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) ListHistory(
	context.Context, automations.ListHistoryParams,
) (automations.AutomationPage[automations.AutomationHistorySummary], error) {
	return automations.AutomationPage[automations.AutomationHistorySummary]{}, errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) InterruptActiveRuns(context.Context, time.Time, string) error {
	return errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) DeleteHistoryBefore(context.Context, time.Time, int) (int64, error) {
	return 0, errRuntimePersistenceUnavailable
}

var errRuntimePersistenceUnavailable = errors.New("automation runtime persistence is not available in D1 tests")

type stubAutomationDevices struct {
	observationError  error
	entityEventError  error
	commandError      error
	normalizedCommand devices.CommandParameters
	observationCalls  []devices.EntityID
	entityEventCalls  []string
	commandCalls      []devices.CommandInput
}

func (stub *stubAutomationDevices) ValidateObservationTrigger(_ context.Context, entityID devices.EntityID) error {
	stub.observationCalls = append(stub.observationCalls, entityID)
	return stub.observationError
}

func (stub *stubAutomationDevices) ValidateEntityEventTrigger(
	_ context.Context, entityID devices.EntityID, name devices.EntityEventName,
) error {
	stub.entityEventCalls = append(stub.entityEventCalls, string(entityID)+"/"+string(name))
	return stub.entityEventError
}

func (stub *stubAutomationDevices) ValidateCommand(
	_ context.Context, input devices.CommandInput,
) (devices.CommandParameters, error) {
	stub.commandCalls = append(stub.commandCalls, input)
	if stub.commandError != nil {
		return nil, stub.commandError
	}
	if stub.normalizedCommand != nil {
		return stub.normalizedCommand, nil
	}
	return input.Parameters, nil
}

func (*stubAutomationDevices) CommandAdmissionOpen() bool { return true }

func (*stubAutomationDevices) ExecuteCommand(
	context.Context, devices.CommandInput,
) (devices.CommandResult, error) {
	return devices.CommandResult{}, errors.New("ExecuteCommand is not available in D1 tests")
}

func (*stubAutomationDevices) GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error) {
	return devices.CommandRecord{}, errors.New("GetCommand is not available in D1 tests")
}

func newAutomationService(
	t *testing.T,
	devicesStub automations.AutomationDevices,
) *automations.Service {
	t.Helper()
	repository := &definitionTestRepository{
		SQLiteRepository: newAutomationRepository(t, openAutomationDatabase(t)),
	}
	return automations.NewService(repository, devicesStub, automations.AutomationDependencies{})
}

// TestServiceCreateAutomationValidatesEveryCurrentReference protects A2: create
// validates each Observation Trigger Entity, each Entity Event Trigger name, and
// each Step Operation and parameters through the devices seam before persisting
// once. It also proves the persisted Step parameters are the devices-normalized
// form rather than the caller's input.
func TestServiceCreateAutomationValidatesEveryCurrentReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	definition := validDomainDefinition(t)
	definition.Triggers = append(definition.Triggers, automations.AutomationTrigger{
		ID:          "single_press",
		Kind:        automations.TriggerKindEntityEvent,
		EntityEvent: &automations.EntityEventTrigger{EntityID: newEntityID(t), EventName: "single_press"},
	})
	normalized := devices.CommandParameters(`{"value":false}`)
	stub := &stubAutomationDevices{normalizedCommand: normalized}
	service := newAutomationService(t, stub)

	record, err := service.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.observationCalls) != 1 ||
		stub.observationCalls[0] != definition.Triggers[0].Observation.EntityID {
		t.Fatalf("observation validation calls = %#v", stub.observationCalls)
	}
	if len(stub.entityEventCalls) != 1 {
		t.Fatalf("entity event validation calls = %#v", stub.entityEventCalls)
	}
	if len(stub.commandCalls) != 1 || stub.commandCalls[0].OperationName != devices.OperationNameSet {
		t.Fatalf("command validation calls = %#v", stub.commandCalls)
	}
	if string(record.Definition.Steps[0].Parameters) != string(normalized) {
		t.Fatalf("stored parameters = %s, want normalized %s",
			record.Definition.Steps[0].Parameters, normalized)
	}
	stored, err := service.GetAutomation(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Definition.Steps[0].Parameters) != string(normalized) {
		t.Fatalf("reloaded parameters = %s, want normalized %s",
			stored.Definition.Steps[0].Parameters, normalized)
	}
}

// TestServiceCreateAutomationRejectsInvalidReferencesAtomically protects A2:
// a missing or wrong-kind Trigger Entity, an unsupported Entity Event name, or
// an unsupported Operation and invalid parameters rejects the whole definition
// and persists nothing.
func TestServiceCreateAutomationRejectsInvalidReferencesAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name string
		stub *stubAutomationDevices
	}{
		{"trigger entity", &stubAutomationDevices{observationError: devices.ErrEntityNotFound}},
		{"wrong kind trigger entity", &stubAutomationDevices{observationError: devices.ErrAutomationTriggerSource}},
		{"unsupported event name", &stubAutomationDevices{entityEventError: devices.ErrAutomationTriggerSource}},
		{"unsupported operation", &stubAutomationDevices{commandError: devices.ErrInvalidCommand}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definition := validDomainDefinition(t)
			definition.Triggers = append(definition.Triggers, automations.AutomationTrigger{
				ID:          "single_press",
				Kind:        automations.TriggerKindEntityEvent,
				EntityEvent: &automations.EntityEventTrigger{EntityID: newEntityID(t), EventName: "single_press"},
			})
			service := newAutomationService(t, test.stub)
			if _, err := service.CreateAutomation(ctx, definition); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("error = %v, want ErrInvalidAutomation", err)
			}
			page, err := service.ListAutomations(ctx, automations.ListAutomationsParams{})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 0 {
				t.Fatalf("invalid definition persisted %d automations", len(page.Items))
			}
		})
	}
}

// TestServiceReplaceAndDeleteGuardExpectedRevision protects A3 through the
// service: replacement and deletion compare the caller's expected revision and
// reject a stale one without mutating the stored definition.
func TestServiceReplaceAndDeleteGuardExpectedRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service := newAutomationService(t, &stubAutomationDevices{})
	created, err := service.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	updated := validDomainDefinition(t)
	updated.Name = "Renamed"
	if _, err = service.ReplaceAutomation(ctx, created.ID, 7, updated); !errors.Is(
		err, automations.ErrRevisionConflict,
	) {
		t.Fatalf("stale replacement error = %v, want ErrRevisionConflict", err)
	}
	stored, err := service.GetAutomation(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 1 || stored.Definition.Name != created.Definition.Name {
		t.Fatalf("stale replacement mutated the definition: %#v", stored)
	}
	replaced, err := service.ReplaceAutomation(ctx, created.ID, 1, updated)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Revision != 2 || replaced.Definition.Name != "Renamed" {
		t.Fatalf("replacement = %#v", replaced)
	}
	if err = service.DeleteAutomation(ctx, created.ID, 1); !errors.Is(err, automations.ErrRevisionConflict) {
		t.Fatalf("stale deletion error = %v, want ErrRevisionConflict", err)
	}
	if err = service.DeleteAutomation(ctx, created.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = service.GetAutomation(ctx, created.ID); !errors.Is(err, automations.ErrAutomationNotFound) {
		t.Fatalf("read after deletion error = %v, want ErrAutomationNotFound", err)
	}
}

// TestServiceRequiresDeviceValidation protects that a definition is never
// persisted when the devices seam is missing, which would otherwise bypass A2.
func TestServiceRequiresDeviceValidation(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, nil)
	if _, err := service.CreateAutomation(
		context.Background(), validDomainDefinition(t),
	); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("error = %v, want ErrInvalidAutomation", err)
	}
}

// TestServiceCreateAutomationRejectsInvalidDefinition protects that structural
// validation still runs before reference validation and persistence.
func TestServiceCreateAutomationRejectsInvalidDefinition(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, &stubAutomationDevices{})
	definition := validDomainDefinition(t)
	definition.Triggers = nil
	if _, err := service.CreateAutomation(
		context.Background(),
		definition,
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("error = %v, want ErrInvalidAutomation", err)
	}
}
