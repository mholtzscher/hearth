package automations //nolint:testpackage // Tests inject identity generation and exercise real SQLite boundaries.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type automationSenderFunc func(context.Context, string, devices.RuntimeID, devices.CommandRequest) (devices.CommandAcceptance, error)

func (send automationSenderFunc) Send(
	ctx context.Context,
	adapter string,
	runtime devices.RuntimeID,
	request devices.CommandRequest,
) (devices.CommandAcceptance, error) {
	return send(ctx, adapter, runtime, request)
}

type automationExecutionHarness struct {
	repo       *SQLiteRepository
	database   *sql.DB
	records    *devices.SQLiteRepository
	devices    *devices.Service
	service    *Service
	power      devices.EntityID
	effect     devices.EntityID
	sends      atomic.Int64
	beforeSend func(context.Context, devices.CommandRequest) error
}

//nolint:gocognit // Real-boundary lifecycle matrix keeps setup and outcome assertions together.
func newAutomationExecutionHarness(t *testing.T) *automationExecutionHarness {
	t.Helper()
	repo, database := testAutomationRepository(t)
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	records := devices.NewSQLiteRepository(database, catalog)
	harness := &automationExecutionHarness{repo: repo, database: database, records: records}
	harness.devices = devices.NewService(
		devices.SQLiteStores(records),
		automationSenderFunc(
			func(ctx context.Context, adapter string, runtime devices.RuntimeID, request devices.CommandRequest) (devices.CommandAcceptance, error) {
				harness.sends.Add(1)
				// An independent read from the sender observes committed intent and Command
				// identities. No sender is invoked inside the automation transaction.
				var stepStatus, marker string
				readErr := database.QueryRowContext(ctx, `SELECT status, reserved_correlation_id FROM automation_run_steps WHERE reserved_command_id = ?`, request.ID).
					Scan(&stepStatus, &marker)
				if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
					return devices.CommandAcceptance{}, readErr
				}
				if readErr == nil && (stepStatus != "running" || marker != string(request.CorrelationID)) {
					t.Errorf("dispatch before matching durable intent: status=%s marker=%s", stepStatus, marker)
				}
				record, readErr := records.GetCommand(ctx, request.ID)
				if readErr != nil {
					return devices.CommandAcceptance{}, readErr
				}
				if record.Status != devices.CommandStatusRequested || record.CorrelationID != request.CorrelationID {
					t.Error("command identities not persisted before send")
				}
				if harness.beforeSend != nil {
					if sendErr := harness.beforeSend(ctx, request); sendErr != nil {
						return devices.CommandAcceptance{}, sendErr
					}
				}
				if request.EntityID == harness.power {
					observationID, idErr := devices.NewObservationID()
					if idErr != nil {
						return devices.CommandAcceptance{}, idErr
					}
					value := devices.Value(`true`)
					if string(request.Parameters) == `{"value":false}` {
						value = devices.Value(`false`)
					}
					_, projectErr := harness.devices.ProjectObservation(ctx, adapter, runtime, devices.Observation{
						ID:                observationID,
						EntityID:          request.EntityID,
						Value:             value,
						AdapterReceivedAt: time.Now().UTC(),
						RefreshForCommand: &request.ID,
					}, time.Now().UTC())
					if projectErr != nil {
						return devices.CommandAcceptance{}, projectErr
					}
				}
				return devices.CommandAcceptance{Accepted: true}, nil
			},
		),
		catalog,
		devices.Dependencies{},
	)
	runtime := devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err = harness.devices.ClaimAdapterRuntime(
		t.Context(),
		devices.ClaimAdapterRuntimeParams{
			AdapterID:       "simulator",
			RuntimeID:       runtime,
			SoftwareName:    "test",
			SoftwareVersion: "1",
		},
	); err != nil {
		t.Fatal(err)
	}
	registration, err := harness.devices.Register(t.Context(), "simulator", runtime, devices.Registration{
		BindingKey: "automation-test",
		Device:     devices.DeviceDescriptor{Name: "Automation test", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{
			{
				Key:        "power",
				ExternalID: "power",
				Name:       "Power",
				TypeID:     devices.EntityTypePowerV1,
				Support:    devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			},
			{
				Key:        "effect",
				ExternalID: "effect",
				Name:       "Effect",
				TypeID:     devices.EntityTypeEnumactionV1,
				Support:    devices.EntitySupport(`{"state":{},"operations":{"trigger":{"values":["blink"]}}}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.power, harness.effect = registration.Entities[0].EntityID, registration.Entities[1].EntityID
	codec, err := NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	timezone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	harness.service = NewService(repo, harness.devices, records, codec, timezone, nil)
	t.Cleanup(func() {
		harness.service.StopAutomationExecutionAdmission()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if waitErr := harness.service.WaitAutomationRuns(ctx); waitErr != nil {
			t.Error(waitErr)
		}
	})
	return harness
}

func (harness *automationExecutionHarness) definition() AutomationDefinition {
	return AutomationDefinition{
		Name:     "Original",
		Triggers: []AutomationTrigger{{ID: "daily", Kind: AutomationTriggerKindCron, Expression: "0 19 * * *"}},
		Steps: []AutomationStep{
			{EntityID: harness.power, OperationName: "set", Parameters: devices.CommandParameters(`{"value":true}`)},
			{
				EntityID:      harness.effect,
				OperationName: "trigger",
				Parameters:    devices.CommandParameters(`{"name":"blink"}`),
			},
		},
	}
}

func createExecutionAutomation(
	t *testing.T,
	harness *automationExecutionHarness,
	definition AutomationDefinition,
) AutomationRecord {
	t.Helper()
	record, err := harness.service.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func startExecutionAutomation(
	t *testing.T,
	harness *automationExecutionHarness,
	id AutomationID,
	key string,
) AutomationAdmission {
	t.Helper()
	admission, err := harness.service.StartManualRun(
		t.Context(),
		AutomationManualRequest{AutomationID: id, IdempotencyKey: key},
	)
	if err != nil {
		t.Fatal(err)
	}
	return admission
}
func waitExecutionRun(t *testing.T, harness *automationExecutionHarness, id AutomationRunID) AutomationRunRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := harness.service.WaitAutomationRuns(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := harness.service.GetAutomationRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}
func receiveAutomationBarrier(t *testing.T, barrier <-chan struct{}) {
	t.Helper()
	select {
	case <-barrier:
	case <-time.After(10 * time.Second):
		t.Fatal("automation barrier not reached")
	}
}

// A1/A5/A6/A9: both outcomes advance only after durable completion; request
// cancellation, edits, and mutable admission responses cannot change the snapshot.
//
//nolint:cyclop,gocyclo // The ordered lifecycle asserts each independent snapshot and outcome invariant.
func TestAutomationOrderedDetachedSnapshot(t *testing.T) {
	t.Parallel()
	harness := newAutomationExecutionHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	harness.beforeSend = func(context.Context, devices.CommandRequest) error {
		if harness.sends.Load() == 1 {
			close(entered)
			<-release
		}
		return nil
	}
	automation := createExecutionAutomation(t, harness, harness.definition())
	ctx, cancel := context.WithCancel(t.Context())
	admission, err := harness.service.StartManualRun(
		ctx,
		AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "original"},
	)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	receiveAutomationBarrier(t, entered)
	blocked, err := harness.service.GetAutomationRun(t.Context(), admission.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Steps[0].Status != AutomationStepStatusRunning ||
		blocked.Steps[1].Status != AutomationStepStatusPending ||
		harness.sends.Load() != 1 {
		t.Fatalf("sequence advanced while first blocked: %#v", blocked)
	}
	if blocked.Steps[0].CommandID == nil || blocked.Steps[0].ReservedCorrelationID == nil {
		t.Fatal("running command evidence missing")
	}
	changed := harness.definition()
	changed.Name = "Edited"
	changed.Steps = []AutomationStep{
		{EntityID: harness.power, OperationName: "set", Parameters: devices.CommandParameters(`{"value":false}`)},
	}
	if _, err = harness.service.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: automation.ID, ExpectedRevision: 1, Definition: changed},
	); err != nil {
		t.Fatal(err)
	}
	admission.Run.Snapshot.Definition.Steps[1].Parameters[0] = '!'
	admission.Run.Snapshot.Definition.Steps[1].EntityID = harness.power
	// Release without closing twice on a fatal path.
	release <- struct{}{}
	run := waitExecutionRun(t, harness, admission.Run.ID)
	if run.Status != AutomationRunStatusSucceeded || run.Snapshot.Revision != 1 ||
		run.Snapshot.Definition.Name != "Original" ||
		run.Snapshot.Timezone != "America/New_York" ||
		len(run.MatchedTriggerIDs) != 0 {
		t.Fatalf("snapshot/outcome = %#v", run)
	}
	if run.Steps[0].Status != AutomationStepStatusSatisfied || run.Steps[0].Outcome == nil ||
		*run.Steps[0].Outcome != devices.OutcomeObserved ||
		run.Steps[1].Status != AutomationStepStatusDispatched ||
		run.Steps[1].Outcome == nil ||
		*run.Steps[1].Outcome != devices.OutcomeDispatched ||
		harness.sends.Load() != 2 {
		t.Fatalf("step outcomes = %#v", run.Steps)
	}
	next := startExecutionAutomation(t, harness, automation.ID, "edited")
	nextRun := waitExecutionRun(t, harness, next.Run.ID)
	if nextRun.Snapshot.Revision != 2 || nextRun.Snapshot.Definition.Name != "Edited" || len(nextRun.Steps) != 1 ||
		string(nextRun.Steps[0].Definition.Parameters) != `{"value":false}` ||
		nextRun.Status != AutomationRunStatusSucceeded {
		t.Fatalf("next snapshot = %#v", nextRun)
	}
	if err = harness.service.DeleteAutomation(t.Context(), automation.ID, 2); err != nil {
		t.Fatal(err)
	}
	reused := startExecutionAutomation(t, harness, automation.ID, "original")
	if !reused.Reused || reused.Run.ID != run.ID || harness.sends.Load() != 3 {
		t.Fatal("deleted definition lost retained invocation")
	}
}

// A5: an old successful Command must never be adopted by a colliding Step,
// even when a faulty identity generator also reuses its correlation marker.
//
//nolint:gocognit // Real-boundary lifecycle matrix keeps setup and outcome assertions together.
func TestAutomationCommandIDCollisionNeverAdoptsOldOutcome(t *testing.T) {
	t.Parallel()
	for _, effect := range []bool{false, true} {
		for _, sameMarker := range []bool{false, true} {
			t.Run(fmt.Sprintf("dispatched=%t/same-marker=%t", effect, sameMarker), func(t *testing.T) {
				t.Parallel()
				harness := newAutomationExecutionHarness(t)
				definition := harness.definition()
				if effect {
					definition.Steps[0] = definition.Steps[1]
				}
				step := definition.Steps[0]
				originalResult, err := harness.devices.ExecuteCommand(
					t.Context(),
					devices.CommandInput{
						EntityID:      step.EntityID,
						OperationName: step.OperationName,
						Parameters:    step.Parameters,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				original, err := harness.records.GetCommand(t.Context(), originalResult.CommandID)
				if err != nil {
					t.Fatal(err)
				}
				harness.service.newCommandID = func() (devices.CommandID, error) { return original.ID, nil }
				if sameMarker {
					harness.service.newCorrelationID = func() (devices.CorrelationID, error) { return original.CorrelationID, nil }
				}
				automation := createExecutionAutomation(t, harness, definition)
				admission := startExecutionAutomation(t, harness, automation.ID, "collision")
				run := waitExecutionRun(t, harness, admission.Run.ID)
				if run.Status != AutomationRunStatusFailed || run.FailureCode == nil ||
					*run.FailureCode != AutomationFailureCommandIDConflict ||
					run.Steps[0].CommandID != nil ||
					run.Steps[0].Outcome != nil ||
					!run.Steps[0].PrecreationFailure ||
					run.Steps[1].Status != AutomationStepStatusNotAttempted ||
					harness.sends.Load() != 1 {
					t.Fatalf("collision adopted old command: %#v", run)
				}
				unchanged, err := harness.records.GetCommand(t.Context(), original.ID)
				if err != nil || !reflect.DeepEqual(original, unchanged) {
					t.Fatalf("old command changed: %#v, %v", unchanged, err)
				}
				if !sameMarker && *run.Steps[0].ReservedCorrelationID == original.CorrelationID {
					t.Fatal("marker was not independent")
				}
			})
		}
	}
}

// A5/A9: intent/result write failures must close both gates and retain claims,
// including after graceful shutdown. A SQL trigger exercises actual rollback.
//
//nolint:gocognit // Real-boundary lifecycle matrix keeps setup and outcome assertions together.
func TestAutomationPersistenceFaultRetainsClaimUntilRecovery(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"intent", "command-outcome", "step-outcome", "run-outcome"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			harness := newAutomationExecutionHarness(t)
			definition := harness.definition()
			definition.Steps[0] = definition.Steps[1] // dispatched: no Observation can complete ahead of the injected failure
			if phase == "run-outcome" {
				definition.Steps = definition.Steps[:1]
			}
			automation := createExecutionAutomation(t, harness, definition)
			trigger := `CREATE TRIGGER injected_fault BEFORE UPDATE ON automation_run_steps WHEN NEW.status = 'running' BEGIN SELECT RAISE(ABORT, 'injected'); END`
			switch phase {
			case "command-outcome":
				trigger = `CREATE TRIGGER injected_fault BEFORE UPDATE ON commands BEGIN SELECT RAISE(ABORT, 'injected'); END`
			case "step-outcome":
				trigger = `CREATE TRIGGER injected_fault BEFORE UPDATE ON automation_run_steps WHEN NEW.status = 'dispatched' BEGIN SELECT RAISE(ABORT, 'injected'); END`
			case "run-outcome":
				trigger = `CREATE TRIGGER injected_fault BEFORE UPDATE ON automation_runs WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'injected'); END`
			}
			if _, err := harness.database.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			admission := startExecutionAutomation(t, harness, automation.ID, "fault")
			run := waitExecutionRun(t, harness, admission.Run.ID)
			if run.Status != AutomationRunStatusRunning || harness.service.AutomationExecutionReady() {
				t.Fatalf("fault manufactured terminality or stayed ready: %#v", run)
			}
			expectedSends := int64(1)
			if phase == "intent" {
				expectedSends = 0
			}
			if harness.sends.Load() != expectedSends {
				t.Fatalf("fault sends = %d, want %d", harness.sends.Load(), expectedSends)
			}
			harness.service.StopAutomationExecutionAdmission()
			if _, err := harness.service.StartManualRun(
				t.Context(),
				AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "new"},
			); !errors.Is(
				err,
				ErrAutomationUnavailable,
			) {
				t.Fatalf("fault admission = %v", err)
			}
			if err := harness.service.DeleteAutomation(
				t.Context(),
				automation.ID,
				1,
			); !errors.Is(
				err,
				ErrAutomationRunActive,
			) {
				t.Fatalf("fault released claim: %v", err)
			}
			if _, err := harness.database.ExecContext(t.Context(), `DROP TRIGGER injected_fault`); err != nil {
				t.Fatal(err)
			}
			if err := harness.records.InterruptActiveCommands(t.Context(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if err := harness.repo.InterruptAutomationRuns(t.Context()); err != nil {
				t.Fatal(err)
			}
			recovered, err := harness.service.GetAutomationRun(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Status != AutomationRunStatusInterrupted || recovered.FailureCode == nil ||
				*recovered.FailureCode != AutomationFailureCoreRestarted ||
				harness.sends.Load() != expectedSends ||
				harness.service.AutomationExecutionReady() {
				t.Fatalf("recovery replayed or cleared process fault: %#v", recovered)
			}
		})
	}
}

// A9: stop closes both gates before waiting and does not cancel the current
// Operation. Completed final Steps succeed; unfinished sequences are interrupted.
//
//nolint:gocognit // Real-boundary lifecycle matrix keeps setup and outcome assertions together.
func TestAutomationShutdownDrainsCurrentStepOnly(t *testing.T) {
	t.Parallel()
	for _, finalStep := range []bool{false, true} {
		t.Run(fmt.Sprintf("final-step=%t", finalStep), func(t *testing.T) {
			t.Parallel()
			harness := newAutomationExecutionHarness(t)
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			harness.beforeSend = func(ctx context.Context, _ devices.CommandRequest) error { close(entered); <-release; return ctx.Err() }
			definition := harness.definition()
			if finalStep {
				definition.Steps = definition.Steps[:1]
			}
			automation := createExecutionAutomation(t, harness, definition)
			admission := startExecutionAutomation(t, harness, automation.ID, "shutdown")
			receiveAutomationBarrier(t, entered)
			harness.service.StopAutomationExecutionAdmission()
			if harness.service.AutomationExecutionReady() {
				t.Fatal("shutdown readiness still true")
			}
			if _, err := harness.service.StartManualRun(
				t.Context(),
				AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "shutdown"},
			); !errors.Is(
				err,
				ErrAutomationUnavailable,
			) {
				t.Fatalf("shutdown admitted run: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := harness.service.WaitAutomationRuns(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("wait completed before current command: %v", err)
			}
			release <- struct{}{}
			run := waitExecutionRun(t, harness, admission.Run.ID)
			if run.Steps[0].Status != AutomationStepStatusSatisfied || harness.sends.Load() != 1 {
				t.Fatalf("drain lost outcome or dispatched later step: %#v", run)
			}
			if finalStep {
				if run.Status != AutomationRunStatusSucceeded {
					t.Fatalf("complete sequence interrupted: %#v", run)
				}
			} else if run.Status != AutomationRunStatusInterrupted || run.FailureCode == nil || *run.FailureCode != AutomationFailureCoreStopping || run.Steps[1].Status != AutomationStepStatusNotAttempted {
				t.Fatalf("unfinished sequence not interrupted: %#v", run)
			}
		})
	}
}

// A9: fault retention belongs to uncertain Runs, not every worker sharing the
// closed gate. A healthy in-flight Command must still persist its known result.
func TestAutomationFaultDoesNotRetainUnrelatedKnownSequence(t *testing.T) {
	t.Parallel()
	harness := newAutomationExecutionHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	harness.beforeSend = func(context.Context, devices.CommandRequest) error {
		close(entered)
		<-release
		return nil
	}
	healthy := createExecutionAutomation(t, harness, harness.definition())
	faulted := createExecutionAutomation(t, harness, harness.definition())
	admission := startExecutionAutomation(t, harness, healthy.ID, "healthy")
	receiveAutomationBarrier(t, entered)
	// Only the other Run's intent fails, after the healthy Command has started.
	if _, err := harness.database.ExecContext(t.Context(), fmt.Sprintf(`
		CREATE TRIGGER isolated_intent_fault BEFORE UPDATE ON automation_run_steps
		WHEN NEW.status = 'running' AND NEW.run_id <> '%s'
		BEGIN SELECT RAISE(ABORT, 'injected'); END`, admission.Run.ID)); err != nil {
		t.Fatal(err)
	}
	uncertain := startExecutionAutomation(t, harness, faulted.ID, "faulted")
	// The fault gate is a deterministic barrier; do not wait for the blocked worker.
	deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for harness.service.AutomationExecutionReady() {
		select {
		case <-deadline.Done():
			t.Fatal("intent failure did not close admission")
		case <-time.After(time.Millisecond):
		}
	}
	harness.service.StopAutomationExecutionAdmission()
	release <- struct{}{}
	run := waitExecutionRun(t, harness, admission.Run.ID)
	if run.Status != AutomationRunStatusInterrupted || run.FailureCode == nil ||
		*run.FailureCode != AutomationFailureCoreStopping ||
		run.Steps[0].Status != AutomationStepStatusSatisfied ||
		run.Steps[1].Status != AutomationStepStatusNotAttempted || harness.sends.Load() != 1 {
		t.Fatalf("known sequence retained or lost result: %#v", run)
	}
	retained := waitExecutionRun(t, harness, uncertain.Run.ID)
	if retained.Status != AutomationRunStatusRunning || retained.CompletedAt != nil {
		t.Fatalf("uncertain Run lost active claim: %#v", retained)
	}
}
