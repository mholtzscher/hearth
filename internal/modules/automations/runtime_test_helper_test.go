package automations_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// scriptedDevices is a controllable AutomationDevices seam. It records every
// ExecuteCommand input so tests can prove ordering and that no retry happened,
// and it serves GetCommand from the commands it durably "created".
type scriptedDevices struct {
	mu sync.Mutex

	admissionOpen  bool
	observationErr error
	entityEventErr error
	block          <-chan struct{}
	onStart        func(devices.CommandInput)
	execute        func(context.Context, devices.CommandInput) (devices.CommandResult, error)
	getCommand     func(context.Context, devices.CommandID) (devices.CommandRecord, error)
	executions     []devices.CommandInput
	commands       map[devices.CommandID]devices.CommandRecord
}

func newScriptedDevices() *scriptedDevices {
	return &scriptedDevices{admissionOpen: true, commands: map[devices.CommandID]devices.CommandRecord{}}
}

func (scripted *scriptedDevices) ValidateObservationTrigger(context.Context, devices.EntityID) error {
	return scripted.observationErr
}

func (scripted *scriptedDevices) ValidateEntityEventTrigger(
	context.Context, devices.EntityID, devices.EntityEventName,
) error {
	return scripted.entityEventErr
}

func (scripted *scriptedDevices) ValidateCommand(
	_ context.Context, input devices.CommandInput,
) (devices.CommandParameters, error) {
	return input.Parameters, nil
}

func (scripted *scriptedDevices) CommandAdmissionOpen() bool {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	return scripted.admissionOpen
}

func (scripted *scriptedDevices) setCommandAdmissionOpen(open bool) {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	scripted.admissionOpen = open
}

func (scripted *scriptedDevices) ExecuteCommand(
	ctx context.Context, input devices.CommandInput,
) (devices.CommandResult, error) {
	scripted.mu.Lock()
	scripted.executions = append(scripted.executions, input)
	execute := scripted.execute
	onStart := scripted.onStart
	block := scripted.block
	scripted.mu.Unlock()
	if onStart != nil {
		onStart(input)
	}
	if block != nil {
		<-block
	}
	if execute != nil {
		return execute(ctx, input)
	}
	completedAt := time.Now().UTC()
	scripted.mu.Lock()
	scripted.commands[input.ID] = devices.CommandRecord{
		ID:            input.ID,
		CorrelationID: input.CorrelationID,
		EntityID:      input.EntityID,
		OperationName: input.OperationName,
		Parameters:    append(devices.CommandParameters(nil), input.Parameters...),
		Status:        devices.CommandStatusSatisfied,
		CompletedAt:   &completedAt,
	}
	scripted.mu.Unlock()
	return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
}

func (scripted *scriptedDevices) GetCommand(
	ctx context.Context, id devices.CommandID,
) (devices.CommandRecord, error) {
	scripted.mu.Lock()
	getCommand := scripted.getCommand
	record, found := scripted.commands[id]
	scripted.mu.Unlock()
	if getCommand != nil {
		return getCommand(ctx, id)
	}
	if !found {
		return devices.CommandRecord{}, devices.ErrCommandNotFound
	}
	record.Parameters = append(devices.CommandParameters(nil), record.Parameters...)
	return record, nil
}

func (scripted *scriptedDevices) recordCommand(record devices.CommandRecord) {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	scripted.commands[record.ID] = record
}

func (scripted *scriptedDevices) executionCount() int {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	return len(scripted.executions)
}

// runtimeTestDependencies is a fixed clock and deterministic identity source so
// tests never depend on wall time or random UUID ordering.
func runtimeTestDependencies() automations.AutomationDependencies {
	return automations.AutomationDependencies{
		Now: func() time.Time { return runtimeTestNow },
	}
}

//nolint:gochecknoglobals // Fixed fixture instant shared by runtime tests.
var runtimeTestNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func newRuntimeService(
	t *testing.T,
	scripted *scriptedDevices,
	dependencies automations.AutomationDependencies,
) (*automations.Service, *sql.DB) {
	t.Helper()
	database := openAutomationDatabase(t)
	repository := automations.NewSQLiteRepository(database, dependencies)
	service := automations.NewService(repository, scripted, dependencies)
	return service, database
}

// runtimeDefinition builds one enabled definition with the requested number of
// ordered Steps and a single Observation Trigger.
func runtimeDefinition(t *testing.T, stepCount int) automations.AutomationDefinition {
	t.Helper()
	definition := automations.AutomationDefinition{
		Name:    "Runtime automation",
		Enabled: true,
		Triggers: []automations.AutomationTrigger{{
			ID:   "trigger",
			Kind: automations.TriggerKindObservation,
			Observation: &automations.ObservationTrigger{
				EntityID:     newEntityID(t),
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
				Comparisons: []automations.ObservationComparison{
					comparison("/temperature", automations.ComparisonGreaterThan, "20"),
				},
			},
		}},
	}
	for index := range stepCount {
		definition.Steps = append(definition.Steps, automations.AutomationStep{
			ID:            automations.StepID(fmt.Sprintf("step_%d", index)),
			EntityID:      newEntityID(t),
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"value":true}`),
		})
	}
	return definition
}

// runtimeDefinitionFor builds a one-Step definition whose Observation Trigger
// matches one exact Entity.
func runtimeDefinitionFor(
	t *testing.T,
	triggerEntity devices.EntityID,
) automations.AutomationDefinition {
	t.Helper()
	definition := runtimeDefinition(t, 1)
	definition.Triggers[0].Observation.EntityID = triggerEntity
	return definition
}

// newObservationFact builds one accepted Observation Fact reporting a matching
// value at the requested emit time.
func newObservationFact(
	t *testing.T,
	entityID devices.EntityID,
	emittedAt time.Time,
) automations.DeviceFact {
	t.Helper()
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return automations.DeviceFact{
		Family: automations.DeviceFactObservation,
		Observation: &automations.ObservationFact{
			FactID:        factID,
			ObservationID: observationID,
			EntityID:      entityID,
			Disposition:   devices.DispositionApplied,
			Value:         devices.Value(`{"temperature":25}`),
			EmittedAt:     emittedAt,
		},
	}
}

func createRuntimeAutomation(
	t *testing.T,
	service *automations.Service,
	definition automations.AutomationDefinition,
) automations.AutomationRecord {
	t.Helper()
	record, err := service.CreateAutomation(context.Background(), definition)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func waitForRuns(t *testing.T, service *automations.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.WaitRuns(ctx); err != nil {
		t.Fatal(err)
	}
}

func historyEntry(
	t *testing.T,
	service *automations.Service,
	automationID automations.AutomationID,
	entryID string,
) automations.AutomationHistoryEntry {
	t.Helper()
	entry, err := service.GetHistoryEntry(context.Background(), automationID, entryID)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func listHistory(
	t *testing.T,
	service *automations.Service,
	automationID automations.AutomationID,
) []automations.AutomationHistorySummary {
	t.Helper()
	page, err := service.ListHistory(context.Background(), automations.ListHistoryParams{AutomationID: automationID})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}
