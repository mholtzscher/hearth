//go:build integration

package automations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// definitionTestRepository uses SQLite for definitions and rejects runtime calls.
type definitionTestRepository struct {
	*automationssqlite.AutomationRepository
}

var _ automations.Repository = (*definitionTestRepository)(nil)

func (*definitionTestRepository) AdmitDeviceFact(
	context.Context, automations.DeviceFact, devices.EntityStateSnapshot, time.Time, time.Time,
) (automations.AdmissionResult, error) {
	return automations.AdmissionResult{}, errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) AdmitManualRun(
	context.Context, automations.ManualRunInput, devices.EntityStateSnapshot, time.Time,
) (automations.ManualAdmissionResult, error) {
	return nil, errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) RecordStep(context.Context, automations.StepExecutionRecord) error {
	return errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) CompleteStep(context.Context, automations.StepExecutionCompletion) error {
	return errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) CompleteRun(context.Context, automations.RunCompletion) error {
	return errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) GetHistoryEntry(
	context.Context, automations.AutomationID, string,
) (automations.HistoryEntry, error) {
	return nil, errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) ListHistory(
	context.Context, automations.ListHistoryParams,
) (automations.Page[automations.HistorySummary], error) {
	return automations.Page[automations.HistorySummary]{}, errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) InterruptActiveRuns(context.Context, time.Time, string) error {
	return errRuntimePersistenceUnavailable
}

func (*definitionTestRepository) DeleteHistoryBefore(context.Context, time.Time, int) (int64, error) {
	return 0, errRuntimePersistenceUnavailable
}

// Saving cron has no Trigger Entity dependency but still validates Steps and Conditions.
// SQLite proves normalized persistence and an invalid replacement leaves the record intact.
func TestServiceSavesCronWithoutTriggerReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stub := &stubAutomationDevices{
		observationError: devices.ErrEntityNotFound,
		entityEventError: devices.ErrEntityNotFound,
	}
	service := newAutomationService(t, stub)
	definition := cronDefinition(t, " \t00  7 * * mon-FRI ")
	conditionEntity := newEntityID(t)
	definition.Conditions = admissionConditionTree(conditionEntity, "20")
	record, err := service.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetAutomation(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Definition.Triggers[0].Body.(automations.CronTrigger).Expression != "00 7 * * mon-FRI" {
		t.Fatalf("stored expression = %q", stored.Definition.Triggers[0].Body.(automations.CronTrigger).Expression)
	}
	if len(stub.observationCalls) != 0 || len(stub.entityEventCalls) != 0 {
		t.Fatal("cron was sent for device Trigger reference validation")
	}
	if len(stub.commandCalls) != 1 || len(stub.conditionCalls) != 1 || stub.conditionCalls[0] != conditionEntity {
		t.Fatalf("command/condition references = %v / %v", stub.commandCalls, stub.conditionCalls)
	}
	cronBody := definition.Triggers[0].Body.(automations.CronTrigger)
	cronBody.Expression = "0 9 * * FRI"
	definition.Triggers[0].Body = cronBody
	replaced, err := service.ReplaceAutomation(ctx, record.ID, record.Revision, definition)
	if err != nil {
		t.Fatal(err)
	}
	cronBody2 := definition.Triggers[0].Body.(automations.CronTrigger)
	cronBody2.Expression = "0 9 1 * FRI"
	definition.Triggers[0].Body = cronBody2
	if _, err = service.ReplaceAutomation(
		ctx,
		record.ID,
		replaced.Revision,
		definition,
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("invalid replacement error = %v", err)
	}
	after, err := service.GetAutomation(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != replaced.Revision ||
		after.Definition.Triggers[0].Body.(automations.CronTrigger).Expression != "0 9 * * FRI" {
		t.Fatalf("invalid replacement changed record: %#v", after)
	}
	stub.conditionError = devices.ErrEntityNotFound
	cronBody3 := definition.Triggers[0].Body.(automations.CronTrigger)
	cronBody3.Expression = "0 9 * * FRI"
	definition.Triggers[0].Body = cronBody3
	if _, err = service.CreateAutomation(ctx, definition); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("missing Condition Entity error = %v", err)
	}
	stub.conditionError = nil
	stub.commandError = devices.ErrEntityNotFound
	if _, err = service.CreateAutomation(ctx, definition); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("missing Step Entity error = %v", err)
	}
}

func newAutomationService(
	t *testing.T,
	devicesStub automations.AutomationDevices,
) *automations.Service {
	t.Helper()
	repository := &definitionTestRepository{
		AutomationRepository: newAutomationRepository(t, openAutomationDatabase(t)),
	}
	return automations.NewService(repository, devicesStub, automations.Dependencies{})
}

// Creation must validate all device references before persisting once, using
// devices-normalized Step parameters.
func TestServiceCreateAutomationValidatesEveryCurrentReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	definition := validDomainDefinition(t)
	definition.Triggers = append(
		definition.Triggers,
		automations.Trigger{
			ID:   "single_press",
			Body: automations.EntityEventTrigger{EntityID: newEntityID(t), EventName: "single_press"},
		},
	)
	normalized := devices.CommandParameters(`{"value":false}`)
	stub := &stubAutomationDevices{normalizedCommand: normalized}
	service := newAutomationService(t, stub)

	record, err := service.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.observationCalls) != 1 ||
		stub.observationCalls[0] != definition.Triggers[0].Body.(automations.ObservationTrigger).EntityID {
		t.Fatalf("observation validation calls = %#v", stub.observationCalls)
	}
	if len(stub.entityEventCalls) != 1 {
		t.Fatalf("entity event validation calls = %#v", stub.entityEventCalls)
	}
	if len(stub.commandCalls) != 1 || stub.commandCalls[0].OperationName != devices.OperationNameSet {
		t.Fatalf("command validation calls = %#v", stub.commandCalls)
	}
	if string(record.Definition.Steps[0].Body.(automations.CommandStep).Parameters) != string(normalized) {
		t.Fatalf("stored parameters = %s, want normalized %s",
			record.Definition.Steps[0].Body.(automations.CommandStep).Parameters, normalized)
	}
	stored, err := service.GetAutomation(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Definition.Steps[0].Body.(automations.CommandStep).Parameters) != string(normalized) {
		t.Fatalf("reloaded parameters = %s, want normalized %s",
			stored.Definition.Steps[0].Body.(automations.CommandStep).Parameters, normalized)
	}
}

// Any invalid Trigger or Step reference must reject the entire definition without writes.
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
			definition.Triggers = append(
				definition.Triggers,
				automations.Trigger{
					ID:   "single_press",
					Body: automations.EntityEventTrigger{EntityID: newEntityID(t), EventName: "single_press"},
				},
			)
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

// This regression test protects save-time pointer validation and fails if an
// API-response path such as /state/value can silently target a scalar value.
func TestServiceCreateAutomationRejectsPointerOutsideCurrentObservationValue(t *testing.T) {
	t.Parallel()
	definition := validDomainDefinition(t)
	observationBody := definition.Triggers[0].Body.(automations.ObservationTrigger)
	observationBody.Comparisons[0].Pointer = "/state/value"
	definition.Triggers[0].Body = observationBody
	stub := &stubAutomationDevices{observationValue: devices.Value(`21600`)}
	service := newAutomationService(t, stub)

	_, err := service.CreateAutomation(context.Background(), definition)
	if !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("error = %v, want ErrInvalidAutomation", err)
	}
	if err == nil || !strings.Contains(err.Error(), "use an empty value_pointer for a scalar") {
		t.Fatalf("error = %v, want scalar pointer guidance", err)
	}
}

// Object-valued Observations still support RFC 6901 paths that select a nested
// member; validation must not collapse every comparison to the scalar form.
func TestServiceCreateAutomationAcceptsPointerInsideCurrentObservationValue(t *testing.T) {
	t.Parallel()
	definition := validDomainDefinition(t)
	stub := &stubAutomationDevices{observationValue: devices.Value(`{"temperature":21}`)}
	service := newAutomationService(t, stub)

	if _, err := service.CreateAutomation(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
}

// Stale revisions must prevent replacement and deletion without changing stored data.
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

// Missing device validation must prevent persistence.
func TestServiceRequiresDeviceValidation(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, nil)
	if _, err := service.CreateAutomation(
		context.Background(), validDomainDefinition(t),
	); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("error = %v, want ErrInvalidAutomation", err)
	}
}

// Structural validation must run before reference validation and persistence.
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
