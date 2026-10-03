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
	"slices"
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
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type branchingHistoryRun struct {
	conditionsRun

	FailureCode     *string `json:"failure_code"`
	BranchDecisions []struct {
		Position         int       `json:"position"`
		StepID           string    `json:"step_id"`
		Outcome          string    `json:"outcome"`
		SelectedBranchID *string   `json:"selected_branch_id"`
		EvaluatedAt      time.Time `json:"evaluated_at"`
		Evaluations      []struct {
			BranchID   *string `json:"branch_id"`
			Evaluation struct {
				EvaluatedAt time.Time `json:"evaluated_at"`
				Result      string    `json:"result"`
				Nodes       []struct {
					ID            string          `json:"id"`
					SelectedValue json.RawMessage `json:"selected_value"`
					Trigger       *struct {
						MatchedTriggerIDs []string `json:"matched_trigger_ids"`
					} `json:"trigger"`
				} `json:"nodes"`
			} `json:"evaluation"`
		} `json:"evaluations"`
	} `json:"branch_decisions"`
}

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
				if queryErr := database.QueryRowContext(commandCtx, "SELECT count(*) FROM automation_run_branch_decisions").
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
		return fmt.Sprintf(`{"id":%q,"entity_id":%q,"operation":"set","parameters":{"value":%t}}`, id, entity, value)
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
	if len(run.BranchDecisions) != 2 {
		t.Fatalf("decisions = %#v", run.BranchDecisions)
	}
	outer, inner := run.BranchDecisions[0], run.BranchDecisions[1]
	if outer.StepID != "route" || outer.Outcome != "branch" || outer.SelectedBranchID == nil ||
		*outer.SelectedBranchID != "on" ||
		len(outer.Evaluations) != 2 ||
		inner.StepID != "nested" ||
		inner.Outcome != "then" {
		t.Fatalf("decisions = %#v", run.BranchDecisions)
	}
	for _, evaluation := range outer.Evaluations {
		if !evaluation.Evaluation.EvaluatedAt.Equal(outer.EvaluatedAt) || len(evaluation.Evaluation.Nodes) != 1 ||
			string(evaluation.Evaluation.Nodes[0].SelectedValue) != "true" {
			t.Fatalf("Choose evidence = %#v", evaluation)
		}
	}
	if !inner.EvaluatedAt.After(outer.EvaluatedAt) ||
		string(inner.Evaluations[0].Evaluation.Nodes[0].SelectedValue) != "false" {
		t.Fatalf("nested evidence = %#v", inner)
	}
	for position, want := range []string{"satisfied", "not_attempted", "satisfied", "satisfied", "satisfied"} {
		if len(run.Steps) != 5 || run.Steps[position].Status != want {
			t.Fatalf("attempts = %#v", run.Steps)
		}
	}
	for _, command := range []struct {
		position int
		entity   string
		value    bool
	}{{0, registered.lightEntityID, true}, {2, registered.lightEntityID, false}, {3, registered.fanEntityID, true}, {4, registered.fanEntityID, false}} {
		if run.Steps[command.position].VerifiedCommandID == nil {
			t.Fatalf("command position %d has no verified link", command.position)
		}
		assertTerminalLinkedCommand(
			ctx,
			t,
			address,
			*run.Steps[command.position].VerifiedCommandID,
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
		len(bypassed.BranchDecisions) != 1 ||
		bypassed.BranchDecisions[0].Outcome != "unknown" ||
		bypassed.Steps[0].Status != "not_attempted" {
		t.Fatalf("bypassed Run = %#v", bypassed)
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
		len(batch.BranchDecisions) != 1 ||
		batch.BranchDecisions[0].Outcome != "error" ||
		len(batch.BranchDecisions[0].Evaluations) != 0 {
		t.Fatalf("immediate batch Run = %#v", batch)
	}
	for _, attempt := range batch.Steps {
		if attempt.Status != "not_attempted" || attempt.VerifiedCommandID != nil {
			t.Fatalf("batch read failure attempted command: %#v", attempt)
		}
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
}

func (repository *branchingDecisionBarrier) RecordBranchDecision(
	ctx context.Context,
	id automations.RunID,
	decision automations.BranchDecision,
) error {
	repository.writes.Add(1)
	if decision.StepID == repository.step {
		close(repository.before)
		<-repository.commit
	}
	if err := repository.Repository.RecordBranchDecision(ctx, id, decision); err != nil {
		return err
	}
	if decision.StepID == repository.step {
		close(repository.after)
		<-repository.resume
		return repository.writeErr
	}
	return nil
}

func (repository *branchingDecisionBarrier) CompleteRun(
	ctx context.Context,
	completion automations.RunCompletion,
) error {
	if completion.Status == automations.RunInterrupted && repository.interruptErr != nil {
		return repository.interruptErr
	}
	return repository.Repository.CompleteRun(ctx, completion)
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
		EntityID:      input.EntityID,
		OperationName: input.OperationName,
		Parameters:    bytes.Clone(input.Parameters),
		Status:        devices.CommandStatusSatisfied,
		CompletedAt:   &now,
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
		&stubDevices{},
		service,
		&stubAgent{},
		&testReadiness{},
		&stubDevices{},
		service,
		newMCPServer(&stubDevices{}, service, nil),
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

// A6/A15: public history is readable on each side of the commit, and actual app
// shutdown joins the blocked worker. A failed interruption leaves startup, not a
// replay worker, responsible for the durable running row.
//
//nolint:gocognit,gocyclo,cyclop // Each lifecycle case crosses the same two transaction barriers.
func TestBranchingDecisionBoundaryShutdownAndStartup(t *testing.T) {
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
			const command = `{"id":"child","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":true}}`
			const condition = `{"id":"manual","kind":"not","child":{"id":"automatic","kind":"trigger","trigger_ids":["trigger"]}}`
			steps := fmt.Sprintf(
				`[{"id":"route","kind":"if","conditions":%s,"then":[%s],"else":[{"id":"unselected","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":false}}]}]`,
				condition,
				command,
			)
			if test.completedChild {
				steps = fmt.Sprintf(
					`[{"id":"first","kind":"if","conditions":%s,"then":[%s]},{"id":"route","kind":"if","conditions":%s,"then":[{"id":"outer-sibling","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":false}}]}]`,
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
			if len(before.BranchDecisions) != wantPrevious || seam.calls != wantPrevious {
				t.Fatalf("before commit: decisions %d, Commands %d", len(before.BranchDecisions), seam.calls)
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
			if len(committed.BranchDecisions) != wantPrevious+1 || committed.Status != "running" ||
				seam.calls != wantPrevious {
				t.Fatalf("committed selection = %#v, Commands %d", committed, seam.calls)
			}
			catalog, err := devices.NewBuiltinTypeCatalog()
			if err != nil {
				t.Fatal(err)
			}
			deviceService := devices.NewService(
				devicessqlite.DeviceStores(devicessqlite.NewDeviceRepository(database, catalog)),
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
			// Close admission synchronously before releasing the committed decision.
			// coreShutdown must then join the worker before withdrawing dependencies.
			if !test.ambiguous {
				service.StopAdmission()
			}
			close(repository.resume)
			if test.ambiguous {
				branchingAwait(t, fault)
				if service.AdmissionOpen() {
					t.Fatal("executor fault left admission open after interruption failed")
				}
				ready := branchingLocalRequest(t, handler, http.MethodGet, "/readyz", "")
				if ready.Code != http.StatusServiceUnavailable {
					t.Fatalf("fault readiness = %d", ready.Code)
				}
			}
			if err = shutdown.run(); err != nil {
				t.Fatal(err)
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
			for position, attempt := range after.Steps {
				if test.completedChild && position == 0 {
					if attempt.Status != "satisfied" || attempt.VerifiedCommandID == nil {
						t.Fatalf("completed child = %#v", attempt)
					}
					continue
				}
				if attempt.Status != "not_attempted" || attempt.VerifiedCommandID != nil {
					t.Fatalf("branch drain changed attempt = %#v", attempt)
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
				!reflect.DeepEqual(recovered.BranchDecisions, committed.BranchDecisions) {
				t.Fatalf("startup Run = %#v", recovered)
			}
			if test.interruptionFails && (recovered.FailureCode == nil || *recovered.FailureCode != "core_restarted") {
				t.Fatalf("recovery reason = %v", recovered.FailureCode)
			}
			for position, attempt := range recovered.Steps {
				if attempt.Status != after.Steps[position].Status ||
					!sameBranchingCommandID(attempt.VerifiedCommandID, after.Steps[position].VerifiedCommandID) {
					t.Fatalf("startup changed attempt %d: %#v", position, attempt)
				}
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

func sameBranchingCommandID(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

// A4/A15: an actual held Observation and app scheduler pass the immutable Trigger
// match into the branch evaluator, not just into the history summary.
//
//nolint:paralleltest // Real adapter outcome deadlines need a serial Core slice under race instrumentation.
func TestBranchingHeldTriggerProvenanceThroughCore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	address, stop, coreErrors := startDeviceFactsCore(
		ctx,
		t,
		server.ClientURL(),
		filepath.Join(t.TempDir(), "hearth.db"),
	)
	defer stopDeviceFactsCore(t, stop, coreErrors)
	waitForCoreHealthz(ctx, t, address, coreErrors)
	registered := startSliceAdapter(ctx, t, server, address)
	defer func() { registered.stop(); _ = registered.session.Close() }()
	definition := fmt.Sprintf(
		`{"name":"Held branch","enabled":true,"triggers":[{"id":"held","kind":"held_state","entity_id":%q,"comparisons":[{"value_pointer":"","operator":"eq","operand":true}],"for_seconds":1}],"steps":[{"id":"route","kind":"if","conditions":{"id":"held-source","kind":"trigger","trigger_ids":["held"]},"then":[{"id":"off","entity_id":%q,"operation":"set","parameters":{"value":false}}]}]}`,
		registered.powerEntityID,
		registered.powerEntityID,
	)
	id := createBranchingAutomation(ctx, t, address, definition)
	if err := registered.publish(ctx); err != nil {
		t.Fatal(err)
	}
	runID := waitForHeldStateHistory(ctx, t, address, id)
	run := readBranchingRun(
		t,
		sliceRequest(ctx, t, http.MethodGet, address, "/v1/automations/"+id+"/history/"+runID, nil),
	)
	assertBranchingTriggerEvidence(t, run, "held_state", []string{"held"}, []string{"held"})
	if len(run.Steps) != 1 || run.Steps[0].Status != "satisfied" || run.Steps[0].VerifiedCommandID == nil {
		t.Fatalf("held command = %#v", run.Steps)
	}
	assertTerminalLinkedCommand(ctx, t, address, *run.Steps[0].VerifiedCommandID, registered.powerEntityID, false)
}

// A4/A15: multiple calendar matches survive the app worker and public history;
// the leaf records its configured-order intersection rather than the whole set.
func TestBranchingScheduledTriggerIntersectionThroughAppWorker(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC).UnixNano())
	dependencies := automations.Dependencies{
		Now:               func() time.Time { return time.Unix(0, clock.Load()).UTC() },
		HouseholdLocation: time.UTC,
		Logger:            slog.New(slog.DiscardHandler),
	}
	repository := &branchingDecisionBarrier{
		Repository: automationssqlite.NewAutomationRepository(openOrderingDatabase(t), dependencies),
		step:       "route",
		before:     make(chan struct{}),
		commit:     make(chan struct{}),
		after:      make(chan struct{}),
		resume:     make(chan struct{}),
	}
	seam := &branchingCommandSeam{
		blockingAutomationDevices: newBlockingAutomationDevices(),
		commands:                  make(map[devices.CommandID]devices.CommandRecord),
	}
	service := automations.NewService(repository, seam, dependencies)
	cleanupBranchingWorker(t, service, repository)
	handler := branchingPublicHandler(service)
	definition := `{"name":"Scheduled intersection","enabled":true,"triggers":[{"id":"a","kind":"cron","expression":"* * * * *"},{"id":"b","kind":"cron","expression":"* * * * *"},{"id":"c","kind":"cron","expression":"0 0 * * *"}],"steps":[{"id":"route","kind":"if","conditions":{"id":"source","kind":"trigger","trigger_ids":["b","c"]},"then":[{"id":"child","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":true}}]}]}`
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
	ticks := make(chan time.Time)
	worker, err := startScheduleScheduling(
		ctx,
		dependencies.Logger,
		service,
		dependencies.Now,
		func() (<-chan time.Time, func()) { return ticks, func() {} },
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if stopErr := worker.Stop(ctx); stopErr != nil {
			t.Error(stopErr)
		}
	}()
	clock.Store(time.Date(2026, 10, 3, 12, 1, 20, 0, time.UTC).UnixNano())
	select {
	case ticks <- time.Time{}:
	case <-ctx.Done():
		t.Fatal("schedule tick not consumed")
	}
	branchingAwait(t, repository.before)
	close(repository.commit)
	branchingAwait(t, repository.after)
	page := branchingLocalRequest(t, handler, http.MethodGet, "/v1/automations/"+record.ID+"/history", "")
	var history struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err = json.Unmarshal(page.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 {
		t.Fatalf("schedule history = %s", page.Body.String())
	}
	historyPath := "/v1/automations/" + record.ID + "/history/" + history.Items[0].ID
	run := readBranchingRun(t, branchingLocalRequest(t, handler, http.MethodGet, historyPath, "").Result())
	assertBranchingTriggerEvidence(t, run, "schedule", []string{"a", "b"}, []string{"b"})
	close(repository.resume)
	if err = service.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if seam.reads.Load() != 0 {
		t.Fatal("scheduled Trigger-only branch read State")
	}
}

func assertBranchingTriggerEvidence(
	t *testing.T,
	run branchingHistoryRun,
	source string,
	matched, intersection []string,
) {
	t.Helper()
	if run.Source != source || !slices.Equal(run.MatchedTriggerIDs, matched) || len(run.BranchDecisions) != 1 {
		t.Fatalf("trigger provenance = %#v", run)
	}
	decision := run.BranchDecisions[0]
	if decision.Outcome != "then" || len(decision.Evaluations) != 1 ||
		len(decision.Evaluations[0].Evaluation.Nodes) != 1 {
		t.Fatalf("trigger decision = %#v", decision)
	}
	node := decision.Evaluations[0].Evaluation.Nodes[0]
	if node.Trigger == nil || !slices.Equal(node.Trigger.MatchedTriggerIDs, intersection) || node.SelectedValue != nil {
		t.Fatalf("trigger evidence = %#v", node)
	}
}
