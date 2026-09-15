package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// apiDevices is a minimal AutomationDevices seam for HTTP behavior tests. It
// validates nothing, serves every created Command as satisfied, and returns the
// State snapshot the test scripted so Condition admission can be exercised
// through real HTTP.
type apiDevices struct {
	mu            sync.Mutex
	admissionOpen bool
	block         <-chan struct{}
	onExecute     func(devices.CommandInput)
	commands      map[devices.CommandID]devices.CommandRecord
	snapshot      devices.EntityStateSnapshot
	snapshotErr   error
	snapshotReads [][]devices.EntityID
}

func newAPIDevices() *apiDevices {
	return &apiDevices{admissionOpen: true, commands: map[devices.CommandID]devices.CommandRecord{}}
}

func (*apiDevices) ValidateObservationTrigger(context.Context, devices.EntityID) error { return nil }

func (*apiDevices) ValidateConditionEntity(context.Context, devices.EntityID) error { return nil }

// GetEntityStateSnapshot serves the scripted snapshot or error and records every
// requested Entity set, so tests can prove a bypass or unconditioned admission
// read no State.
func (stub *apiDevices) GetEntityStateSnapshot(
	_ context.Context, ids []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.snapshotReads = append(stub.snapshotReads, append([]devices.EntityID(nil), ids...))
	if stub.snapshotErr != nil {
		return devices.EntityStateSnapshot{}, stub.snapshotErr
	}
	if stub.snapshot.Entries == nil {
		return devices.EntityStateSnapshot{}, nil
	}
	return stub.snapshot, nil
}

// setEntityStateSnapshot scripts the next snapshot returned through the seam.
func (stub *apiDevices) setEntityStateSnapshot(snapshot devices.EntityStateSnapshot) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.snapshot = snapshot
	stub.snapshotErr = nil
}

// setEntityStateSnapshotError scripts a failing snapshot read.
func (stub *apiDevices) setEntityStateSnapshotError(err error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.snapshotErr = err
}

// snapshotRequests returns the recorded requested Entity sets in order.
func (stub *apiDevices) snapshotRequests() [][]devices.EntityID {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([][]devices.EntityID(nil), stub.snapshotReads...)
}

// executionCount returns how many Step Commands the seam has served.
func (stub *apiDevices) executionCount() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return len(stub.commands)
}

func (*apiDevices) ValidateEntityEventTrigger(context.Context, devices.EntityID, devices.EntityEventName) error {
	return nil
}

func (*apiDevices) ValidateCommand(
	_ context.Context, input devices.CommandInput,
) (devices.CommandParameters, error) {
	return input.Parameters, nil
}

func (stub *apiDevices) CommandAdmissionOpen() bool {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.admissionOpen
}

func (stub *apiDevices) ExecuteCommand(
	_ context.Context, input devices.CommandInput,
) (devices.CommandResult, error) {
	stub.mu.Lock()
	block, onExecute := stub.block, stub.onExecute
	stub.mu.Unlock()
	if onExecute != nil {
		onExecute(input)
	}
	if block != nil {
		<-block
	}
	completedAt := time.Now().UTC()
	stub.mu.Lock()
	stub.commands[input.ID] = devices.CommandRecord{
		ID:            input.ID,
		CorrelationID: input.CorrelationID,
		EntityID:      input.EntityID,
		OperationName: input.OperationName,
		Parameters:    append(devices.CommandParameters(nil), input.Parameters...),
		Status:        devices.CommandStatusSatisfied,
		CompletedAt:   &completedAt,
	}
	stub.mu.Unlock()
	return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
}

func (stub *apiDevices) GetCommand(
	_ context.Context, id devices.CommandID,
) (devices.CommandRecord, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	record, found := stub.commands[id]
	if !found {
		return devices.CommandRecord{}, devices.ErrCommandNotFound
	}
	return record, nil
}

func openAutomationTestDatabase(t *testing.T) *sql.DB {
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

func newAutomationService(t *testing.T, stub *apiDevices) *automations.Service {
	t.Helper()
	database := openAutomationTestDatabase(t)
	dependencies := automations.AutomationDependencies{}
	repository := automationssqlite.NewAutomationRepository(database, dependencies)
	service := automations.NewService(repository, stub, dependencies)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Drain(ctx); err != nil {
			t.Errorf("drain automations: %v", err)
		}
	})
	return service
}

// definitionDocument renders one valid strict definition document with one
// Observation Trigger and the requested number of Steps.
func definitionDocument(t *testing.T, stepCount int) string {
	t.Helper()
	triggerEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	var steps strings.Builder
	for index := range stepCount {
		actionEntity, entityErr := devices.NewEntityID()
		if entityErr != nil {
			t.Fatal(entityErr)
		}
		if index > 0 {
			steps.WriteString(",")
		}
		fmt.Fprintf(
			&steps,
			`{"id":"step_%d","entity_id":%q,"operation":"set","parameters":{"value":true}}`,
			index, string(actionEntity),
		)
	}
	return fmt.Sprintf(`{
		"name": "  Office light  ",
		"enabled": true,
		"triggers": [
			{"id":"warm","kind":"observation","entity_id":%q,"dispositions":["applied"],
			 "comparisons":[{"pointer":"/temperature","operator":"gt","operand":20}]}
		],
		"steps": [%s]
	}`, string(triggerEntity), steps.String())
}

// waitForAPI observes a terminal Run without closing admission for later requests.
func waitForAPI(t *testing.T, service *automations.Service, automationID, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		entry, err := service.GetHistoryEntry(ctx, automations.AutomationID(automationID), runID)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Run != nil && entry.Run.Status != automations.RunRunning {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Run %s did not finish: %v", runID, ctx.Err())
		case <-ticker.C:
		}
	}
}

var _ automationsapi.Automations = (*automations.Service)(nil)
