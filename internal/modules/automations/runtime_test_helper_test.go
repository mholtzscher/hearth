package automations_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// scriptedDevices is a controllable AutomationDevices seam. It records every
// ExecuteCommand input so tests can prove ordering and that no retry happened,
// and it serves GetCommand from the commands it durably "created".
type scriptedDevices struct {
	mu sync.Mutex

	admissionOpen  bool
	observationErr error
	conditionErr   error
	entityEventErr error
	snapshotErr    error
	snapshot       devices.EntityStateSnapshot
	onSnapshotRead func()
	block          <-chan struct{}
	onStart        func(devices.CommandInput)
	execute        func(context.Context, devices.CommandInput) (devices.CommandResult, error)
	getCommand     func(context.Context, devices.CommandID) (devices.CommandRecord, error)
	executions     []devices.CommandInput
	snapshotReads  [][]devices.EntityID
	commands       map[devices.CommandID]devices.CommandRecord
}

func newScriptedDevices() *scriptedDevices {
	return &scriptedDevices{admissionOpen: true, commands: map[devices.CommandID]devices.CommandRecord{}}
}

func (scripted *scriptedDevices) ValidateObservationTrigger(context.Context, devices.EntityID) error {
	return scripted.observationErr
}

func (scripted *scriptedDevices) ValidateConditionEntity(context.Context, devices.EntityID) error {
	return scripted.conditionErr
}

func (scripted *scriptedDevices) ValidateEntityEventTrigger(
	context.Context, devices.EntityID, devices.EntityEventName,
) error {
	return scripted.entityEventErr
}

// GetEntityStateSnapshot serves the configured coherent State snapshot and
// records every requested Entity set, so tests can prove which Entities one
// admission needed and that a replacement read never merges samples.
func (scripted *scriptedDevices) GetEntityStateSnapshot(
	_ context.Context, ids []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	scripted.mu.Lock()
	scripted.snapshotReads = append(scripted.snapshotReads, append([]devices.EntityID(nil), ids...))
	snapshotErr := scripted.snapshotErr
	snapshot := copyEntityStateSnapshot(scripted.snapshot)
	onRead := scripted.onSnapshotRead
	scripted.mu.Unlock()
	// The hook runs without the lock so a test can advance its clock or install
	// a definition before the following admission transaction.
	if onRead != nil {
		onRead()
	}
	if snapshotErr != nil {
		return devices.EntityStateSnapshot{}, snapshotErr
	}
	return snapshot, nil
}

// setEntityStateSnapshot installs the snapshot every later read returns.
func (scripted *scriptedDevices) setEntityStateSnapshot(snapshot devices.EntityStateSnapshot) {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	scripted.snapshot = snapshot
}

// setEntityStateSnapshotError makes every later read fail with err.
func (scripted *scriptedDevices) setEntityStateSnapshotError(err error) {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	scripted.snapshotErr = err
}

func (scripted *scriptedDevices) snapshotRequests() [][]devices.EntityID {
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	requests := make([][]devices.EntityID, len(scripted.snapshotReads))
	for index, ids := range scripted.snapshotReads {
		requests[index] = append([]devices.EntityID(nil), ids...)
	}
	return requests
}

// copyEntityStateSnapshot owns the JSON bytes of every entry so a returned
// snapshot cannot alias the configured fixture.
func copyEntityStateSnapshot(snapshot devices.EntityStateSnapshot) devices.EntityStateSnapshot {
	cloned := devices.EntityStateSnapshot{
		Entries: make(map[devices.EntityID]devices.EntityStateSnapshotEntry, len(snapshot.Entries)),
	}
	for id, entry := range snapshot.Entries {
		copied := devices.EntityStateSnapshotEntry{EntityID: entry.EntityID, Exists: entry.Exists}
		if entry.State != nil {
			state := *entry.State
			state.Value = append(devices.Value(nil), entry.State.Value...)
			copied.State = &state
		}
		cloned.Entries[id] = copied
	}
	return cloned
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
// tests never depend on wall time or random UUID ordering. The fixed retention
// window lets retention tests prune against the same fixture clock.
func runtimeTestDependencies() automations.AutomationDependencies {
	return automations.AutomationDependencies{
		Now:              func() time.Time { return runtimeTestNow },
		HistoryRetention: runtimeTestHistoryRetention,
	}
}

// runtimeTestHistoryRetention is the terminal history window runtime tests
// inject; it clears the module's eight-day safety floor.
const runtimeTestHistoryRetention = 30 * 24 * time.Hour

//nolint:gochecknoglobals // Fixed fixture instant shared by runtime tests.
var runtimeTestNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// openAutomationDatabase opens one migrated Core database in a temporary
// directory, so Automation SQLite tests exercise the real schema.
func openAutomationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := platformdb.Open(context.Background(), filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

// newAutomationRepository builds the SQLite Automation repository with
// production-default dependencies.
func newAutomationRepository(t *testing.T, database *sql.DB) *automationssqlite.AutomationRepository {
	t.Helper()
	return automationssqlite.NewAutomationRepository(database, automations.AutomationDependencies{})
}

func newRuntimeService(
	t *testing.T,
	scripted *scriptedDevices,
	dependencies automations.AutomationDependencies,
) (*automations.Service, *sql.DB) {
	t.Helper()
	database := openAutomationDatabase(t)
	repository := automationssqlite.NewAutomationRepository(database, dependencies)
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
	if err := automations.WaitForRunWorkers(ctx, service); err != nil {
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
