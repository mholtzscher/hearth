//go:build e2e

package hearthd //nolint:testpackage // Tests exercise app assembly and lifecycle boundaries.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func readBranchingRun(t *testing.T, response *http.Response) branchingHistoryRun {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history = %d: %s", response.StatusCode, readSliceBody(t, response))
	}
	var entry struct {
		Kind string               `json:"kind"`
		Run  *branchingHistoryRun `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != "run" || entry.Run == nil {
		t.Fatalf("history is not a Run: %#v", entry)
	}
	return *entry.Run
}

func createBranchingAutomation(ctx context.Context, t *testing.T, address, definition string) string {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodPost, address, "/v1/automations", strings.NewReader(definition))
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d: %s", response.StatusCode, readSliceBody(t, response))
	}
	var record struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&record); err != nil {
		t.Fatal(err)
	}
	return record.ID
}

// A6/A9/A12: the actual Command handler reads committed history before answering.
// Commands change real Core State, so retaining admission State or reading nested
// predicates eagerly changes the selected path or fails the Run.
//
//nolint:gocognit,gocyclo,cyclop,paralleltest // Real adapter outcome deadlines need a serial Core slice under race instrumentation.
func TestBranchingFreshStateAndAdmissionIsolationThroughCore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	path := filepath.Join(t.TempDir(), "hearth.db")
	address, stop, coreErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), path)
	defer stopDeviceFactsCore(t, stop, coreErrors)
	waitForCoreHealthz(ctx, t, address, coreErrors)
	database, err := platformdb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	evidence := make(chan int, 8)
	registered := startConditionsAdapter(
		ctx,
		t,
		server,
		address,
		func(handler adapter.CommandHandler) adapter.CommandHandler {
			return func(commandCtx context.Context, input adapter.Command, responder adapter.Responder) error {
				var count int
				if queryErr := database.QueryRowContext(commandCtx, "SELECT count(*) FROM automation_step_executions WHERE kind IN ('if', 'choose')").
					Scan(&count); queryErr != nil {
					return queryErr
				}
				evidence <- count
				return handler(commandCtx, input, responder)
			}
		},
	)
	defer func() { registered.stop(); _ = registered.session.Close() }()
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	reads := devicessqlite.NewDeviceRepository(database, catalog)
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		snapshot, readErr := reads.GetEntityStateSnapshot(
			ctx,
			[]devices.EntityID{
				devices.EntityID(registered.illuminanceEntityID),
				devices.EntityID(registered.lightEntityID),
				devices.EntityID(registered.fanEntityID),
				devices.EntityID(registered.motionEntityID),
			},
		)
		if readErr != nil {
			return false, readErr
		}
		for _, entry := range snapshot.Entries {
			if entry.State == nil {
				return false, nil
			}
		}
		return true, nil
	})
	// The numeric Entity's materialized value no longer agrees with its backing
	// Observation. Any batch requesting it must fail, not silently return false.
	if _, err = database.ExecContext(
		ctx,
		"UPDATE entity_states SET value_json = '999' WHERE entity_id = ?",
		registered.illuminanceEntityID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = reads.GetEntityStateSnapshot(
		ctx,
		[]devices.EntityID{devices.EntityID(registered.illuminanceEntityID)},
	); !errors.Is(
		err,
		devices.ErrEntityStateSnapshotCorrupt,
	) {
		t.Fatalf("poisoned State read = %v", err)
	}

	command := func(id, entity string, value bool) string {
		return fmt.Sprintf(
			`{"kind":"command", "id":%q,"entity_id":%q,"operation":"set","parameters":{"value":%t}}`,
			id,
			entity,
			value,
		)
	}
	state := func(id, entity string, value bool) string {
		return fmt.Sprintf(
			`{"id":%q,"kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":%t}`,
			id,
			entity,
			value,
		)
	}
	unselected := fmt.Sprintf(
		`{"id":"unselected","kind":"if","conditions":{"id":"poison","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"lt","operand":30},"then":[%s]}`,
		registered.illuminanceEntityID,
		command("unselected-command", registered.fanEntityID, false),
	)
	nested := fmt.Sprintf(
		`{"id":"nested","kind":"if","conditions":%s,"then":[%s]}`,
		state("now-off", registered.lightEntityID, false),
		command("nested-command", registered.fanEntityID, true),
	)
	definition := fmt.Sprintf(
		`{"name":"Fresh branch evidence","enabled":true,"triggers":[{"id":"motion","kind":"observation","entity_id":%q,"dispositions":["applied"],"comparisons":[{"value_pointer":"","operator":"eq","operand":true}]}],"conditions":%s,"steps":[%s,{"id":"route","kind":"choose","branches":[{"id":"off","conditions":%s,"steps":[%s]},{"id":"on","conditions":%s,"steps":[%s,%s]}]},%s]}`,
		registered.motionEntityID,
		state("admission-off", registered.lightEntityID, false),
		command("before", registered.lightEntityID, true),
		state("still-off", registered.lightEntityID, false),
		unselected,
		state("now-on", registered.lightEntityID, true),
		command("selected", registered.lightEntityID, false),
		nested,
		command("outer", registered.fanEntityID, false),
	)
	id := createBranchingAutomation(ctx, t, address, definition)
	response := conditionsManualRun(ctx, t, address, id, "")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("manual = %d: %s", response.StatusCode, readSliceBody(t, response))
	}
	runID := conditionsLocationRunID(t, response)
	response.Body.Close()
	waitForAutomationRun(ctx, t, address, id)
	run := readBranchingRun(
		t,
		sliceRequest(ctx, t, http.MethodGet, address, "/v1/automations/"+id+"/history/"+runID, nil),
	)
	if run.Status != "succeeded" {
		if run.FailureCode != nil {
			t.Logf("Run failure = %s", *run.FailureCode)
		}
		t.Fatalf("Run = %+v, dispatches = %d", run, len(evidence))
	}
	decisions := branchingDecisions(t, run)
	if len(decisions) != 2 {
		t.Fatalf("decisions = %#v", run.Executions)
	}
	outer, inner := decisions[0], decisions[1]
	if outer.execution.StepID != "route" || outer.evidence.Outcome != "branch" ||
		outer.evidence.SelectedBranchID == nil ||
		*outer.evidence.SelectedBranchID != "on" ||
		len(outer.evidence.Evaluations) != 2 ||
		inner.execution.StepID != "nested" ||
		inner.evidence.Outcome != "then" {
		t.Fatalf("decisions = %#v", run.Executions)
	}
	for _, evaluation := range outer.evidence.Evaluations {
		if !evaluation.Evaluation.EvaluatedAt.Equal(outer.execution.StartedAt) ||
			len(evaluation.Evaluation.Nodes) != 1 ||
			string(evaluation.Evaluation.Nodes[0].SelectedValue) != "true" {
			t.Fatalf("Choose evidence = %#v", evaluation)
		}
	}
	if !inner.execution.StartedAt.After(outer.execution.StartedAt) ||
		string(inner.evidence.Evaluations[0].Evaluation.Nodes[0].SelectedValue) != "false" {
		t.Fatalf("nested evidence = %#v", inner)
	}
	if _, _, reached := branchingExecutionForStep(t, run.Executions, "unselected"); reached {
		t.Fatalf("unselected nested command was reached: %#v", run.Executions)
	}
	for _, command := range []struct {
		stepID string
		entity string
		value  bool
	}{{"before", registered.lightEntityID, true}, {"selected", registered.lightEntityID, false}, {"nested-command", registered.fanEntityID, true}, {"outer", registered.fanEntityID, false}} {
		execution, evidence, reached := branchingExecutionForStep(t, run.Executions, command.stepID)
		if !reached || execution.Kind != "command" || execution.Phase != "succeeded" ||
			evidence.VerifiedCommandID == nil {
			t.Fatalf("command %q execution lacks verified link: %#v", command.stepID, execution)
		}
		assertTerminalLinkedCommand(
			ctx,
			t,
			address,
			*evidence.VerifiedCommandID,
			command.entity,
			command.value,
		)
	}
	for _, want := range []int{0, 1, 2, 2} {
		select {
		case got := <-evidence:
			if got != want {
				t.Fatalf("decisions visible at dispatch = %d, want %d", got, want)
			}
		case <-ctx.Done():
			t.Fatal("missing dispatch evidence")
		}
	}

	// Bypass skips a false admission predicate, but missing branch State still
	// fails a Run and never dispatches the selected child.
	bypassDefinition := fmt.Sprintf(
		`{"name":"Bypass is admission only","enabled":true,"triggers":[{"id":"motion","kind":"observation","entity_id":%q,"dispositions":["applied"]}],"conditions":%s,"steps":[{"id":"missing-state","kind":"if","conditions":%s,"then":[%s]}]}`,
		registered.motionEntityID,
		state("admission-on", registered.lightEntityID, true),
		state("absent", registered.otherRoomEntityID, false),
		command("must-not-run", registered.fanEntityID, true),
	)
	bypassID := createBranchingAutomation(ctx, t, address, bypassDefinition)
	blocked := conditionsManualRun(ctx, t, address, bypassID, "")
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("normal admission = %d: %s", blocked.StatusCode, readSliceBody(t, blocked))
	}
	problem := decodeConditionsProblem(t, blocked)
	if problem.Code != "conditions_false" {
		t.Fatalf("normal admission blocked for wrong reason: %#v", problem)
	}
	response = conditionsManualRun(ctx, t, address, bypassID, `{"bypass_conditions":true}`)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("bypass = %d: %s", response.StatusCode, readSliceBody(t, response))
	}
	bypassRunID := conditionsLocationRunID(t, response)
	response.Body.Close()
	waitForAutomationRun(ctx, t, address, bypassID)
	bypassed := readBranchingRun(
		t,
		sliceRequest(ctx, t, http.MethodGet, address, "/v1/automations/"+bypassID+"/history/"+bypassRunID, nil),
	)
	if bypassed.Status != "failed" || bypassed.FailureCode == nil ||
		*bypassed.FailureCode != "branch_condition_unknown" ||
		bypassed.ConditionDecision.Mode != "bypassed" ||
		len(branchingDecisions(t, bypassed)) != 1 ||
		branchingDecisions(t, bypassed)[0].evidence.Outcome != "unknown" {
		t.Fatalf("bypassed Run = %#v", bypassed)
	}
	if _, _, reached := branchingExecutionForStep(t, bypassed.Executions, "must-not-run"); reached {
		t.Fatalf("bypassed branch unexpectedly reached child: %#v", bypassed.Executions)
	}
	select {
	case count := <-evidence:
		t.Fatalf("branch failure dispatched Command with %d decisions", count)
	default:
	}

	// Unlike an unselected nested predicate, a later immediate alternative is
	// included in Choose's batch even when the first Trigger-only arm is true.
	batchDefinition := fmt.Sprintf(
		`{"name":"Immediate batch coverage","enabled":true,"triggers":[{"id":"motion","kind":"observation","entity_id":%q,"dispositions":["applied"]}],"steps":[{"id":"batch","kind":"choose","branches":[{"id":"manual","conditions":{"id":"not-triggered","kind":"not","child":{"id":"source","kind":"trigger","trigger_ids":["motion"]}},"steps":[%s]},{"id":"later","conditions":{"id":"poison","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"lt","operand":30},"steps":[%s]}]}]}`,
		registered.motionEntityID,
		command("first-arm", registered.fanEntityID, true),
		registered.illuminanceEntityID,
		command("later-arm", registered.fanEntityID, false),
	)
	batchID := createBranchingAutomation(ctx, t, address, batchDefinition)
	response = conditionsManualRun(ctx, t, address, batchID, "")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("batch admission = %d: %s", response.StatusCode, readSliceBody(t, response))
	}
	batchRunID := conditionsLocationRunID(t, response)
	response.Body.Close()
	waitForAutomationRun(ctx, t, address, batchID)
	batch := readBranchingRun(
		t,
		sliceRequest(ctx, t, http.MethodGet, address, "/v1/automations/"+batchID+"/history/"+batchRunID, nil),
	)
	if batch.Status != "failed" || batch.FailureCode == nil || *batch.FailureCode != "branch_state_corrupt" ||
		len(branchingDecisions(t, batch)) != 1 ||
		branchingDecisions(t, batch)[0].evidence.Outcome != "error" ||
		len(branchingDecisions(t, batch)[0].evidence.Evaluations) != 0 {
		t.Fatalf("immediate batch Run = %#v", batch)
	}
	if _, _, reached := branchingExecutionForStep(t, batch.Executions, "first-arm"); reached {
		t.Fatalf("batch read failure reached first command: %#v", batch.Executions)
	}
	if _, _, reached := branchingExecutionForStep(t, batch.Executions, "later-arm"); reached {
		t.Fatalf("batch read failure reached later command: %#v", batch.Executions)
	}
	select {
	case count := <-evidence:
		t.Fatalf("batch read failure dispatched Command with %d decisions", count)
	default:
	}
}

// branchingDecisionBarrier wraps the existing repository contract. Both gates
// surround the real transaction; neither manufactures evidence or command order.
type branchingDecisionBarrier struct {
	automations.Repository

	step         automations.StepID
	before       chan struct{}
	commit       chan struct{}
	after        chan struct{}
	resume       chan struct{}
	writeErr     error
	interruptErr error
	writes       atomic.Int64
	interrupts   atomic.Int32
}

func (repository *branchingDecisionBarrier) RecordStep(
	ctx context.Context,
	record automations.StepExecutionRecord,
) error {
	_, isDecision := record.Evidence.(automations.BranchDecided)
	if !isDecision {
		return repository.Repository.RecordStep(ctx, record)
	}
	repository.writes.Add(1)
	if record.StepID == repository.step {
		close(repository.before)
		<-repository.commit
	}
	if err := repository.Repository.RecordStep(ctx, record); err != nil {
		return err
	}
	if record.StepID == repository.step {
		close(repository.after)
		<-repository.resume
		return repository.writeErr
	}
	return nil
}

func (repository *branchingDecisionBarrier) InterruptRun(
	ctx context.Context,
	id automations.RunID,
	at time.Time,
) error {
	repository.interrupts.Add(1)
	if repository.interruptErr != nil {
		return repository.interruptErr
	}
	return repository.Repository.InterruptRun(ctx, id, at)
}

type branchingCommandSeam struct {
	*blockingAutomationDevices

	commands map[devices.CommandID]devices.CommandRecord
	calls    int
	reads    atomic.Int64
}

func (seam *branchingCommandSeam) GetEntityStateSnapshot(
	context.Context,
	[]devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	seam.reads.Add(1)
	return devices.EntityStateSnapshot{}, errors.New("Trigger-only construct must not read State")
}

type branchingFaultSignal struct {
	slog.Handler

	fault chan struct{}
}

func (handler branchingFaultSignal) Enabled(context.Context, slog.Level) bool { return true }

func (handler branchingFaultSignal) Handle(_ context.Context, record slog.Record) error {
	record.Attrs(func(attribute slog.Attr) bool {
		if attribute.Key == "event" && attribute.Value.String() == "automation.executor_fault" {
			select {
			case handler.fault <- struct{}{}:
			default:
			}
		}
		return true
	})
	return nil
}

func (seam *branchingCommandSeam) ExecuteCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandResult, error) {
	seam.calls++
	now := time.Now().UTC()
	seam.commands[input.ID] = devices.CommandRecord{
		ID:            input.ID,
		CorrelationID: input.CorrelationID,
		Status:        devices.CommandStatusSatisfied,
		CompletedAt:   &now,
		EntityID:      input.EntityID,
		OperationName: input.OperationName,
		Parameters:    bytes.Clone(input.Parameters),
	}
	return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
}

func (seam *branchingCommandSeam) GetCommand(_ context.Context, id devices.CommandID) (devices.CommandRecord, error) {
	record, ok := seam.commands[id]
	if !ok {
		return devices.CommandRecord{}, devices.ErrCommandNotFound
	}
	return record, nil
}

func branchingAwait(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal("decision boundary did not arrive")
	}
}

func cleanupBranchingWorker(t *testing.T, service *automations.Service, repository *branchingDecisionBarrier) {
	t.Helper()
	t.Cleanup(func() {
		service.StopAdmission()
		for _, gate := range []chan struct{}{repository.commit, repository.resume} {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := service.Drain(ctx); err != nil {
			t.Error(err)
		}
	})
}

func branchingPublicHandler(service *automations.Service) http.Handler {
	handler, _ := newHTTPHandler(
		nil,
		&stubDevices{},
		service,
		&stubNotifications{},
		&stubDevices{},
		&stubFunctions{},
		&stubAgent{},
		&testReadiness{},
		&stubDevices{},
		service,
		&stubFunctions{},
		mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion}),
	)
	return handler
}

func branchingLocalRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// A6/A15: public history distinguishes committed selection from execution and
// preserves that evidence through interruption and real Core startup. A failed
// interruption leaves startup responsible for the durable running row, without replay.
//
//nolint:gocognit,gocyclo,cyclop // Each lifecycle case crosses the same two transaction barriers.
func TestBranchingDecisionEvidenceSurvivesShutdownAndStartup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                                         string
		completedChild, ambiguous, interruptionFails bool
	}{
		{name: "committed before child"},
		{name: "completed child pending outer sibling", completedChild: true},
		{name: "ambiguous commit then startup recovery", ambiguous: true, interruptionFails: true},
		{name: "completed child then startup recovery", completedChild: true, ambiguous: true, interruptionFails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "hearth.db")
			database := dbtest.OpenMigrated(t, path)
			repository := &branchingDecisionBarrier{
				Repository: automationssqlite.NewAutomationRepository(database, automations.Dependencies{}),
				step:       "route",
				before:     make(chan struct{}),
				commit:     make(chan struct{}),
				after:      make(chan struct{}),
				resume:     make(chan struct{}),
			}
			if test.ambiguous {
				repository.writeErr = errors.New("commit result lost")
			}
			if test.interruptionFails {
				repository.interruptErr = errors.New("interruption unavailable")
			}
			seam := &branchingCommandSeam{
				blockingAutomationDevices: newBlockingAutomationDevices(),
				commands:                  make(map[devices.CommandID]devices.CommandRecord),
			}
			fault := make(chan struct{}, 2)
			service := automations.NewService(
				repository,
				seam,
				automations.Dependencies{
					Logger: slog.New(branchingFaultSignal{Handler: slog.DiscardHandler, fault: fault}),
				},
			)
			cleanupBranchingWorker(t, service, repository)
			handler := branchingPublicHandler(service)
			if ready := branchingLocalRequest(t, handler, http.MethodGet, "/readyz", ""); ready.Code != http.StatusOK {
				t.Fatalf("initial readiness = %d", ready.Code)
			}
			const command = `{"kind":"command", "id":"child","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":true}}`
			const condition = `{"id":"manual","kind":"not","child":{"id":"automatic","kind":"trigger","trigger_ids":["trigger"]}}`
			steps := fmt.Sprintf(
				`[{"id":"route","kind":"if","conditions":%s,"then":[%s],"else":[{"kind":"command", "id":"unselected","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":false}}]}]`,
				condition,
				command,
			)
			if test.completedChild {
				steps = fmt.Sprintf(
					`[{"id":"first","kind":"if","conditions":%s,"then":[%s]},{"id":"route","kind":"if","conditions":%s,"then":[{"kind":"command", "id":"outer-sibling","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":false}}]}]`,
					condition,
					command,
					condition,
				)
			}
			definition := fmt.Sprintf(
				`{"name":"Boundary lifecycle","enabled":true,"triggers":[{"id":"trigger","kind":"observation","entity_id":"ent_01920000-0000-7000-8000-000000000004","dispositions":["applied"]}],"steps":%s}`,
				steps,
			)
			created := branchingLocalRequest(t, handler, http.MethodPost, "/v1/automations", definition)
			if created.Code != http.StatusCreated {
				t.Fatalf("create = %d: %s", created.Code, created.Body.String())
			}
			var record struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(created.Body.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			started := branchingLocalRequest(t, handler, http.MethodPost, "/v1/automations/"+record.ID+"/runs", "")
			if started.Code != http.StatusAccepted {
				t.Fatalf("start = %d: %s", started.Code, started.Body.String())
			}
			historyPath := started.Header().Get("Location")
			branchingAwait(t, repository.before)
			before := readBranchingRun(t, branchingLocalRequest(t, handler, http.MethodGet, historyPath, "").Result())
			wantPrevious := 0
			if test.completedChild {
				wantPrevious = 1
			}
			if len(branchingDecisions(t, before)) != wantPrevious || seam.calls != wantPrevious {
				t.Fatalf("before commit: decisions %d, Commands %d", len(branchingDecisions(t, before)), seam.calls)
			}
			busy := branchingLocalRequest(t, handler, http.MethodPost, "/v1/automations/"+record.ID+"/runs", "")
			if busy.Code != http.StatusConflict || !strings.Contains(busy.Body.String(), "automation_busy") {
				t.Fatalf("concurrent invocation = %d: %s", busy.Code, busy.Body.String())
			}
			close(repository.commit)
			branchingAwait(t, repository.after)
			committed := readBranchingRun(
				t,
				branchingLocalRequest(t, handler, http.MethodGet, historyPath, "").Result(),
			)
			if len(branchingDecisions(t, committed)) != wantPrevious+1 || committed.Status != "running" ||
				seam.calls != wantPrevious {
				t.Fatalf("committed selection = %#v, Commands %d", committed, seam.calls)
			}
			catalog, err := devices.NewBuiltinTypeCatalog()
			if err != nil {
				t.Fatal(err)
			}
			deviceService := devices.NewService(
				testDeviceStores(devicessqlite.NewDeviceRepository(database, catalog), database),
				nil,
				catalog,
				devices.Dependencies{},
			)
			shutdown := &coreShutdown{
				runContext:        ctx,
				logger:            slog.New(slog.DiscardHandler),
				automationService: service,
				deviceService:     deviceService,
			}
			// Stop at the committed decision to inspect interruption evidence.
			// automations_drain_order_test.go owns blocked-worker shutdown ordering.
			if !test.ambiguous {
				service.StopAdmission()
			}
			close(repository.resume)
			if test.ambiguous {
				branchingAwait(t, fault)
				if !service.AdmissionOpen() {
					t.Fatal("Run fault closed independent admission after interruption failed")
				}
				ready := branchingLocalRequest(t, handler, http.MethodGet, "/readyz", "")
				if ready.Code != http.StatusOK {
					t.Fatalf("fault readiness = %d", ready.Code)
				}
				busy = branchingLocalRequest(t, handler, http.MethodPost, "/v1/automations/"+record.ID+"/runs", "")
				if busy.Code != http.StatusConflict || !strings.Contains(busy.Body.String(), "automation_busy") {
					t.Fatalf(
						"failed cleanup must retain affected Automation's busy evidence = %d: %s",
						busy.Code,
						busy.Body.String(),
					)
				}
			}
			if err = shutdown.run(); err != nil {
				t.Fatal(err)
			}
			if test.ambiguous && repository.interrupts.Load() != 1 {
				t.Fatalf("fault cleanup attempted %d times", repository.interrupts.Load())
			}
			after := readBranchingRun(t, branchingLocalRequest(t, handler, http.MethodGet, historyPath, "").Result())
			if seam.calls != wantPrevious || repository.writes.Load() != int64(wantPrevious+1) {
				t.Fatalf("Commands %d, decision writes %d", seam.calls, repository.writes.Load())
			}
			wantStatus, wantCode := "interrupted", "core_stopping"
			if test.ambiguous {
				wantCode = "executor_fault"
			}
			if test.interruptionFails {
				wantStatus = "running"
			}
			if after.Status != wantStatus ||
				(!test.interruptionFails && (after.FailureCode == nil || *after.FailureCode != wantCode)) {
				t.Fatalf("shutdown Run = %#v", after)
			}
			if test.completedChild {
				if commandID := requireVerifiedCommandLink(t, after.Executions, "child"); commandID == "" {
					t.Fatal("completed child command link is empty")
				}
			}
			for _, stepID := range []string{"child", "unselected", "outer-sibling"} {
				if test.completedChild && stepID == "child" {
					continue
				}
				if _, _, reached := branchingExecutionForStep(t, after.Executions, stepID); reached {
					t.Fatalf("branch drain reached pending Step %q: %#v", stepID, after.Executions)
				}
			}
			if service.AdmissionOpen() {
				t.Fatal("shutdown/fault left admission open")
			}
			if seam.reads.Load() != 0 {
				t.Fatal("Trigger-only branch read State")
			}
			if err = database.Close(); err != nil {
				t.Fatal(err)
			}
			server := startLifecycleNATSServer(t)
			address, stop, coreErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), path)
			defer stopDeviceFactsCore(t, stop, coreErrors)
			waitForCoreHealthz(ctx, t, address, coreErrors)
			waitForCoreReady(ctx, t, address, coreErrors)
			recovered := readBranchingRun(t, sliceRequest(ctx, t, http.MethodGet, address, historyPath, nil))
			if recovered.Status != "interrupted" ||
				!reflect.DeepEqual(recovered.Executions, after.Executions) {
				t.Fatalf("startup Run = %#v", recovered)
			}
			if test.interruptionFails && (recovered.FailureCode == nil || *recovered.FailureCode != "core_restarted") {
				t.Fatalf("recovery reason = %v", recovered.FailureCode)
			}
			var count int
			db, openErr := platformdb.Open(ctx, path)
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer db.Close()
			if err = db.QueryRowContext(ctx, "SELECT count(*) FROM commands").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("startup dispatched %d Commands", count)
			}
		})
	}
}
