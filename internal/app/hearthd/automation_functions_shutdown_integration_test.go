//go:build integration

package hearthd //nolint:testpackage // Exercises the real Core shutdown owner with SQLite and SDK barriers.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// The decorator pauses before entering the Function service, after Automation
// reservation has returned. It supplies neither invocation evidence nor execution behavior.
type automationFunctionInvocationBarrier struct {
	automations.AutomationFunctions

	reserved chan adapters.ReservedInvokeInput
	resume   <-chan struct{}
}

func (functions *automationFunctionInvocationBarrier) InvokeReserved(
	ctx context.Context,
	input adapters.ReservedInvokeInput,
) (adapters.Invocation, error) {
	functions.reserved <- input
	<-functions.resume
	return functions.AutomationFunctions.InvokeReserved(ctx, input)
}

// A8: close public gates while a committed reservation has not yet entered the
// Function service, and while that same call is already inside the SDK. In both
// cases the real shutdown owner must join Automations before closing internal
// Function admission or withdrawing dependencies. The following Function leaf
// must remain unreached even after the first succeeds authoritatively.
//
//nolint:gocognit,gocyclo,cyclop // Each lifecycle case crosses the same reservation and real SDK barriers.
func TestAutomationFunctionsShutdownPreservesReservedWork(t *testing.T) {
	t.Parallel()
	for _, inFlight := range []bool{false, true} {
		t.Run(fmt.Sprintf("in_flight=%t", inFlight), func(t *testing.T) {
			t.Parallel()
			fixture := newFunctionIntegrationFixture(t)
			close(fixture.registrar.release)
			entered := make(chan adapter.FunctionRequest, 4)
			release := make(chan struct{})
			resume := make(chan struct{})
			var releaseOnce, resumeOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }); resumeOnce.Do(func() { close(resume) }) }
			t.Cleanup(unblock)
			_, err := fixture.session.StartFunctionServer(t.Context(), []adapter.Function{automationTestFunction(
				func(_ context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
					entered <- request
					<-release
					return adapter.FunctionSucceeded{Result: json.RawMessage(automationFunctionOutput)}
				})})
			if err != nil {
				t.Fatal(err)
			}
			function, err := fixture.service.GetFunction(t.Context(), "function-only", "inspect")
			if err != nil {
				t.Fatal(err)
			}
			functions := &automationFunctionInvocationBarrier{
				AutomationFunctions: fixture.service,
				reserved:            make(chan adapters.ReservedInvokeInput, 4), resume: resume,
			}
			dependencies := automations.Dependencies{Functions: functions}
			repository := automationssqlite.NewAutomationRepository(fixture.database, dependencies)
			service := automations.NewService(repository, newBlockingAutomationDevices(), dependencies)
			t.Cleanup(func() { unblock(); _ = service.Drain(context.Background()) })
			definition, err := automations.DecodeDefinition(json.RawMessage(fmt.Sprintf(
				`{"name":"Shutdown reservation","enabled":true,"triggers":[{"id":"trigger","kind":"observation","entity_id":"ent_01920000-0000-7000-8000-000000000004","dispositions":["applied"]}],"steps":[{"id":"first","kind":"function","adapter_id":"function-only","function_key":"inspect","contract_fingerprint":%q,"arguments":%s},{"id":"must-not-run","kind":"function","adapter_id":"function-only","function_key":"inspect","contract_fingerprint":%q,"arguments":%s}]}`,
				function.ContractFingerprint,
				automationFunctionArguments,
				function.ContractFingerprint,
				automationFunctionArguments,
			)))
			if err != nil {
				t.Fatal(err)
			}
			record, err := service.CreateAutomation(t.Context(), definition)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.StartManualRun(t.Context(), automations.ManualRunInput{AutomationID: record.ID})
			if err != nil {
				t.Fatal(err)
			}
			var input adapters.ReservedInvokeInput
			select {
			case input = <-functions.reserved:
			case <-time.After(10 * time.Second):
				t.Fatal("reservation did not commit")
			}
			entry, err := service.GetHistoryEntry(t.Context(), record.ID, string(run.ID))
			if err != nil {
				t.Fatal(err)
			}
			retained, ok := entry.(automations.Run)
			if !ok || len(retained.Executions) != 1 || retained.Executions[0].StepID != "first" {
				t.Fatalf("Function call preceded durable reservation: %+v", entry)
			}
			reservation, ok := retained.Executions[0].Details.(automations.FunctionExecutionDetails)
			if !ok || reservation.InvocationID != input.InvocationID {
				t.Fatalf("reservation: %+v", retained.Executions[0])
			}
			if _, running := reservation.State.(automations.FunctionRunning); !running {
				t.Fatalf("Function reservation was not running before Invoke: %+v", reservation)
			}
			var request adapter.FunctionRequest
			if inFlight {
				resumeOnce.Do(func() { close(resume) })
				request = awaitAutomationFunctionSDKRequest(t, entered)
			} else if _, readErr := fixture.service.GetInvocation(t.Context(), reservation.InvocationID); !errors.Is(readErr, adapters.ErrInvocationNotFound) {
				t.Fatalf("reservation dispatched before barrier release: %v", readErr)
			}
			dependencyContext, cancelDependencies := context.WithCancel(t.Context())
			t.Cleanup(cancelDependencies)
			var withdrawn atomic.Int32
			shutdown := &coreShutdown{runContext: t.Context(), logger: slog.New(slog.DiscardHandler),
				automationService: service, functionService: fixture.service, cancelDependencies: cancelDependencies,
				transports: []registeredDrain{{stage: "test_function_dependencies", drain: drainFunc(func() error {
					withdrawn.Add(1)
					return nil
				})}},
			}
			finished := make(chan error, 1)
			go func() { finished <- shutdown.run() }()
			waitForMatrixCondition(t, 5*time.Second, func() (bool, error) { return !service.AdmissionOpen(), nil })
			// A valid one-off public invocation is rejected, not mistaken for an
			// invalid catalog/input. Internal admission must still be open here.
			notificationRequest(
				t,
				fixture.handler,
				http.MethodPost,
				"/v1/adapters/function-only/functions/inspect/invocations",
				fmt.Sprintf(
					`{"catalog_revision":%q,"arguments":%s}`,
					function.CatalogRevision,
					automationFunctionArguments,
				),
				http.StatusServiceUnavailable,
			)
			if fixture.service.AdmissionOpen() || dependencyContext.Err() != nil || withdrawn.Load() != 0 {
				t.Fatal("shutdown left public admission open or withdrew dependencies before the Run joined")
			}
			if err = fixture.database.PingContext(t.Context()); err != nil {
				t.Fatalf("SQLite withdrawn before reserved work settled: %v", err)
			}
			select {
			case err = <-finished:
				t.Fatalf("shutdown returned before the reserved call settled: %v", err)
			default:
			}
			if !inFlight {
				resumeOnce.Do(func() { close(resume) })
				request = awaitAutomationFunctionSDKRequest(t, entered)
			}
			if request.InvocationID != string(reservation.InvocationID) || request.FunctionKey != "inspect" ||
				string(request.Arguments) != automationFunctionArguments || request.Deadline.IsZero() {
				t.Fatalf("reserved SDK payload: %+v", request)
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case err = <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("shutdown did not join reserved Function work")
			}
			if dependencyContext.Err() == nil || withdrawn.Load() != 1 || fixture.service.AdmissionOpen() {
				t.Fatal("shutdown did not finally close internal admission and dependencies")
			}
			entry, err = service.GetHistoryEntry(t.Context(), record.ID, string(run.ID))
			if err != nil {
				t.Fatal(err)
			}
			retained, ok = entry.(automations.Run)
			if !ok || len(retained.Executions) != 1 || retained.Executions[0].StepID != "first" {
				t.Fatalf("shutdown reached following Function or lost reservation: %+v", entry)
			}
			completedFunction, ok := retained.Executions[0].Details.(automations.FunctionExecutionDetails)
			if !ok || completedFunction.InvocationID != reservation.InvocationID {
				t.Fatalf("reserved Function evidence: %+v", retained.Executions[0])
			}
			if _, ok = completedFunction.State.(automations.FunctionSucceeded); !ok {
				t.Fatalf("reserved Function did not retain success: %+v", completedFunction)
			}
			if string(completedFunction.Output) != automationFunctionOutput || completedFunction.ValueFailure != nil {
				t.Fatalf("shutdown lost the usable copied output: %+v", completedFunction)
			}
			completed, ok := retained.State.(automations.CompletedRun)
			interrupted, interruptedOK := completed.Outcome.(automations.InterruptedRun)
			if !ok || !interruptedOK || interrupted.FailureCode != "core_stopping" {
				t.Fatalf("shutdown Run: %+v", retained.State)
			}
			invocation, err := fixture.service.GetInvocation(t.Context(), reservation.InvocationID)
			if err != nil || invocation.Completion == nil ||
				string(invocation.Arguments) != automationFunctionArguments {
				t.Fatalf("authoritative Invocation after drain: %+v %v", invocation, err)
			}
			result, ok := invocation.Completion.Evidence.(adapters.Succeeded)
			if !ok || string(result.Result) != automationFunctionOutput {
				t.Fatalf("authoritative successful result: %+v", invocation.Completion)
			}
			select {
			case extra := <-entered:
				t.Fatalf("following Step executed: %+v", extra)
			default:
			}
		})
	}
}

// A8's pre-reservation case uses a durably reached native Delay as the barrier,
// not a sleep. Shutdown wakes that wait and must not reserve the Function leaf.
func TestAutomationFunctionsShutdownBeforeReservation(t *testing.T) {
	t.Parallel()
	fixture := newFunctionIntegrationFixture(t)
	close(fixture.registrar.release)
	entered := make(chan adapter.FunctionRequest, 1)
	_, err := fixture.session.StartFunctionServer(t.Context(), []adapter.Function{automationTestFunction(
		func(_ context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
			entered <- request
			return adapter.FunctionSucceeded{Result: request.Arguments}
		})})
	if err != nil {
		t.Fatal(err)
	}
	function, err := fixture.service.GetFunction(t.Context(), "function-only", "inspect")
	if err != nil {
		t.Fatal(err)
	}
	dependencies := automations.Dependencies{Functions: fixture.service}
	service := automations.NewService(automationssqlite.NewAutomationRepository(fixture.database, dependencies),
		newBlockingAutomationDevices(), dependencies)
	t.Cleanup(func() { _ = service.Drain(context.Background()) })
	definition, err := automations.DecodeDefinition(json.RawMessage(fmt.Sprintf(
		`{"name":"Stop before Function","enabled":true,"triggers":[{"id":"trigger","kind":"observation","entity_id":"ent_01920000-0000-7000-8000-000000000004","dispositions":["applied"]}],"steps":[{"id":"barrier","kind":"delay","duration_ms":86400000},{"id":"must-not-run","kind":"function","adapter_id":"function-only","function_key":"inspect","contract_fingerprint":%q,"arguments":%s}]}`,
		function.ContractFingerprint,
		automationFunctionArguments,
	)))
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartManualRun(t.Context(), automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		entry, readErr := service.GetHistoryEntry(t.Context(), record.ID, string(run.ID))
		if readErr != nil {
			return false, readErr
		}
		retained, ok := entry.(automations.Run)
		if !ok || len(retained.Executions) != 1 || retained.Executions[0].StepID != "barrier" {
			return false, nil
		}
		delay, ok := retained.Executions[0].Details.(automations.DelayExecutionDetails)
		_, running := delay.State.(automations.DelayRunning)
		return ok && running, nil
	})
	shutdown := &coreShutdown{runContext: t.Context(), logger: slog.New(slog.DiscardHandler),
		automationService: service, functionService: fixture.service}
	if err = shutdown.run(); err != nil {
		t.Fatal(err)
	}
	entry, err := service.GetHistoryEntry(t.Context(), record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained, ok := entry.(automations.Run)
	if !ok || len(retained.Executions) != 1 || retained.Executions[0].StepID != "barrier" {
		t.Fatalf("shutdown reached an unreserved Function: %+v", entry)
	}
	delay, ok := retained.Executions[0].Details.(automations.DelayExecutionDetails)
	state, interruptedDelay := delay.State.(automations.DelayInterrupted)
	if !ok || !interruptedDelay || state.FailureCode != "core_stopping" {
		t.Fatalf("shutdown delay evidence: %+v", retained.Executions[0])
	}
	completed, ok := retained.State.(automations.CompletedRun)
	interrupted, interruptedOK := completed.Outcome.(automations.InterruptedRun)
	if !ok || !interruptedOK || interrupted.FailureCode != "core_stopping" {
		t.Fatalf("pre-reservation shutdown outcome: %+v", retained.State)
	}
	select {
	case request := <-entered:
		t.Fatalf("unreserved Function reached SDK: %+v", request)
	default:
	}
}

func awaitAutomationFunctionSDKRequest(t *testing.T, entered <-chan adapter.FunctionRequest) adapter.FunctionRequest {
	t.Helper()
	select {
	case request := <-entered:
		return request
	case <-time.After(10 * time.Second):
		t.Fatal("reserved Function never reached SDK")
		return adapter.FunctionRequest{}
	}
}
