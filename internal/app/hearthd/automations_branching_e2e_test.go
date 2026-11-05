//go:build e2e

package hearthd //nolint:testpackage // Tests exercise app assembly and lifecycle boundaries.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

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
	path := dbtest.MigratedPath(t)
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
