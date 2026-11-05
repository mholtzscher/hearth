//go:build e2e

package hearthd //nolint:testpackage // Public journeys exercise production Core and the Adapter SDK.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// reusableRun deliberately reads the public wire format, not backend models.
type reusableRun struct {
	ID                string     `json:"id"`
	Status            string     `json:"status"`
	FailureCode       string     `json:"failure_code"`
	StartedAt         time.Time  `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	MatchedTriggerIDs []string   `json:"matched_trigger_ids"`
	Cause             struct {
		Kind               string `json:"kind"`
		ParentAutomationID string `json:"parent_automation_id"`
		ParentRunID        string `json:"parent_run_id"`
		ParentStepID       string `json:"parent_step_id"`
	} `json:"cause"`
	Snapshot struct {
		Name     string            `json:"name"`
		Enabled  bool              `json:"enabled"`
		Triggers []json.RawMessage `json:"triggers"`
		Steps    []struct {
			ID           string `json:"id"`
			Kind         string `json:"kind"`
			AutomationID string `json:"automation_id"`
		} `json:"steps"`
	} `json:"snapshot"`
	Executions []reusableExecution `json:"executions"`
}

type reusableExecution struct {
	StepID      string     `json:"step_id"`
	Kind        string     `json:"kind"`
	Phase       string     `json:"phase"`
	Sequence    int        `json:"sequence"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Evidence    struct {
		ChildRunID string `json:"child_run_id"`
		CommandID  string `json:"command_id"`
		ReasonCode string `json:"reason_code"`
	} `json:"evidence"`
}

func reusableHistory(t *testing.T, client *http.Client, core *functionCoreRun, automationID, runID string) reusableRun {
	t.Helper()
	raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet,
		"/v1/automations/"+automationID+"/history/"+runID, "")
	entry := decodeNotification[struct {
		Kind string      `json:"kind"`
		Run  reusableRun `json:"run"`
	}](t, raw)
	if entry.Kind != "run" || entry.Run.ID != runID || entry.Run.Executions == nil {
		t.Fatalf("invalid Run detail: %s", raw)
	}
	return entry.Run
}

func reusableStart(t *testing.T, core *functionCoreRun, id string) string {
	t.Helper()
	response := conditionsManualRun(t.Context(), t, core.address, id, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("manual admission: %d %s", response.StatusCode, readSliceBody(t, response))
	}
	return conditionsLocationRunID(t, response)
}

func reusableAwaitSubAutomation(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	parentID, runID string,
) (reusableRun, string) {
	t.Helper()
	var run reusableRun
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		run = reusableHistory(t, client, core, parentID, runID)
		return len(run.Executions) == 1 && run.Executions[0].Kind == "sub_automation" &&
			run.Executions[0].Phase == "running" && run.Executions[0].Evidence.ChildRunID != "", nil
	})
	if run.Status != "running" || run.Executions[0].StepID != "call" || run.Executions[0].Sequence != 0 {
		t.Fatalf("parent did not wait at SubAutomation: %+v", run)
	}
	return run, run.Executions[0].Evidence.ChildRunID
}

func reusableAssertChild(t *testing.T, child reusableRun, parentID, parentRun string) {
	t.Helper()
	if child.Cause.Kind != "invocation" || child.Cause.ParentAutomationID != parentID ||
		child.Cause.ParentRunID != parentRun || child.Cause.ParentStepID != "call" ||
		child.MatchedTriggerIDs == nil || len(child.MatchedTriggerIDs) != 0 ||
		child.Snapshot.Name != "Reusable child" || child.Snapshot.Enabled ||
		child.Snapshot.Triggers == nil || len(child.Snapshot.Triggers) != 0 {
		t.Fatalf("child provenance or immutable definition: %+v", child)
	}
}

func reusableCreateParent(t *testing.T, core *functionCoreRun, childID, entityID string) string {
	t.Helper()
	return createBranchingAutomation(t.Context(), t, core.address, fmt.Sprintf(
		`{"name":"Parent","enabled":false,"triggers":[],"steps":[{"id":"call","kind":"sub_automation","automation_id":%q},{"id":"parent-set","kind":"command","entity_id":%q,"operation":"set","parameters":{"value":false}}]}`,
		childID,
		entityID,
	))
}

func reusableAssertOnlyRun(t *testing.T, client *http.Client, core *functionCoreRun, automationID, runID string) {
	t.Helper()
	raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet,
		"/v1/automations/"+automationID+"/history", "")
	page := decodeNotification[struct {
		Items []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"items"`
	}](t, raw)
	if len(page.Items) != 1 || page.Items[0].ID != runID || page.Items[0].Kind != "run" {
		t.Fatalf("unexpected invocation history: %s", raw)
	}
}

// A4/A12: while a child Function is held, the actual HTTP history proves the
// parent has committed its child link but has not dispatched its next Command.
// The released child changes State before the parent changes it back.
//
//nolint:gocyclo,cyclop // One public journey verifies the linked execution boundary end to end.
func TestReusableAutomationPublicJourney(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	core := startFunctionCore(t, Config{HouseholdTimezone: "UTC", NATSURL: broker.ClientURL(),
		SQLitePath: dbtest.MigratedPath(t), Agent: requiredAgentConfig(t)})
	client := newNonPoolingHTTPClient(t)
	source := startSliceAdapter(t.Context(), t, broker, core.address)
	t.Cleanup(source.stop)
	session, err := adapter.Connect(
		t.Context(),
		adapter.Config{
			AdapterID:       "reusable-barrier",
			SoftwareName:    "synthetic-reusable-journey",
			SoftwareVersion: "1",
			NATSURL:         broker.ClientURL(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	function := expressionIntegrationFunction(
		func(_ context.Context, _ adapter.FunctionRequest) adapter.FunctionResult {
			close(entered)
			<-release
			return adapter.FunctionSucceeded{Result: json.RawMessage(automationFunctionOutput)}
		},
	)
	if _, err = session.StartFunctionServer(t.Context(), []adapter.Function{function}); err != nil {
		t.Fatal(err)
	}
	discovery := decodeNotification[automationFunctionDiscovery](t, successfulFunctionCoreRequest(t, client, core,
		http.MethodGet, "/v1/adapters/reusable-barrier/functions/inspect", ""))
	childID := createBranchingAutomation(t.Context(), t, core.address, fmt.Sprintf(
		`{"name":"Reusable child","enabled":false,"triggers":[],"steps":[{"id":"hold","kind":"function","adapter_id":"reusable-barrier","function_key":"inspect","contract_fingerprint":%q,"arguments":%s},{"id":"child-set","kind":"command","entity_id":%q,"operation":"set","parameters":{"value":true}}]}`,
		discovery.ContractFingerprint,
		functionExactObject,
		source.powerEntityID,
	))
	parentID := reusableCreateParent(t, core, childID, source.powerEntityID)
	parentRun := reusableStart(t, core, parentID)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("child Function did not enter")
	}
	parent, childRun := reusableAwaitSubAutomation(t, client, core, parentID, parentRun)
	child := reusableHistory(t, client, core, childID, childRun)
	reusableAssertChild(t, child, parentID, parentRun)
	if len(parent.Snapshot.Steps) != 2 || parent.Snapshot.Steps[0].Kind != "sub_automation" ||
		parent.Snapshot.Steps[0].AutomationID != childID || len(child.Snapshot.Steps) != 2 ||
		child.Snapshot.Steps[0].ID != "hold" || child.Snapshot.Steps[1].ID != "child-set" {
		t.Fatalf("incorrect immutable snapshots: parent=%+v child=%+v", parent.Snapshot, child.Snapshot)
	}
	if child.Status != "running" || len(child.Executions) != 1 || child.Executions[0].StepID != "hold" ||
		conditionsCommandCount(t.Context(), t, core.address, source.powerEntityID) != 0 {
		t.Fatalf("effects crossed held child: parent=%+v child=%+v", parent, child)
	}
	unblock()
	awaitAutomationFunctionRun(t, client, core, parentID, map[string]bool{})
	parent = reusableHistory(t, client, core, parentID, parentRun)
	child = reusableHistory(t, client, core, childID, childRun)
	reusableAssertChild(t, child, parentID, parentRun)
	if parent.Status != "succeeded" || child.Status != "succeeded" || len(parent.Executions) != 2 ||
		len(child.Executions) != 2 {
		t.Fatalf("unfinished trace: parent=%+v child=%+v", parent, child)
	}
	subAutomation, next, childCommand := parent.Executions[0], parent.Executions[1], child.Executions[1]
	if subAutomation.Kind != "sub_automation" || subAutomation.Phase != "succeeded" ||
		subAutomation.Evidence.ChildRunID != childRun ||
		next.StepID != "parent-set" || next.Kind != "command" || next.Sequence != 1 || next.Phase != "succeeded" ||
		childCommand.StepID != "child-set" || childCommand.Kind != "command" ||
		childCommand.Sequence != 1 || childCommand.Phase != "succeeded" ||
		child.CompletedAt == nil || subAutomation.CompletedAt == nil || childCommand.CompletedAt == nil ||
		child.CompletedAt.Before(*childCommand.CompletedAt) ||
		subAutomation.CompletedAt.Before(*child.CompletedAt) ||
		next.StartedAt.Before(*subAutomation.CompletedAt) {
		t.Fatalf("SubAutomation did not wait for durable child success: parent=%+v child=%+v", parent, child)
	}
	assertTerminalLinkedCommand(
		t.Context(),
		t,
		core.address,
		childCommand.Evidence.CommandID,
		source.powerEntityID,
		true,
	)
	assertTerminalLinkedCommand(t.Context(), t, core.address, next.Evidence.CommandID, source.powerEntityID, false)
	if count := conditionsCommandCount(t.Context(), t, core.address, source.powerEntityID); count != 2 {
		t.Fatalf("command count = %d", count)
	}
	reusableAssertOnlyRun(t, client, core, parentID, parentRun)
	reusableAssertOnlyRun(t, client, core, childID, childRun)
}

// A9: shutdown joins the nested long wait and commits interruption. Restarting
// the same production database must not resume the child or the caller's next Step.
func TestReusableAutomationShutdownAndRestartNoReplay(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	config := Config{HouseholdTimezone: "UTC", NATSURL: broker.ClientURL(),
		SQLitePath: dbtest.MigratedPath(t), Agent: requiredAgentConfig(t)}
	first := startFunctionCore(t, config)
	client := newNonPoolingHTTPClient(t)
	source := startSliceAdapter(t.Context(), t, broker, first.address)
	t.Cleanup(source.stop)
	childID := createBranchingAutomation(t.Context(), t, first.address, fmt.Sprintf(
		`{"name":"Reusable child","enabled":false,"triggers":[],"steps":[{"id":"wait","kind":"delay","duration_ms":86400000},{"id":"child-set","kind":"command","entity_id":%q,"operation":"set","parameters":{"value":true}}]}`,
		source.powerEntityID,
	))
	parentID := reusableCreateParent(t, first, childID, source.powerEntityID)
	parentRun := reusableStart(t, first, parentID)
	_, childRun := reusableAwaitSubAutomation(t, client, first, parentID, parentRun)
	var child reusableRun
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		child = reusableHistory(t, client, first, childID, childRun)
		return child.Status == "running" && len(child.Executions) == 1 &&
			child.Executions[0].Kind == "delay" && child.Executions[0].Phase == "running", nil
	})
	reusableAssertChild(t, child, parentID, parentRun)
	first.stop()
	first.join(t)
	second := startFunctionCore(t, config)
	parent := reusableHistory(t, client, second, parentID, parentRun)
	child = reusableHistory(t, client, second, childID, childRun)
	reusableAssertChild(t, child, parentID, parentRun)
	if parent.Status != "interrupted" || parent.FailureCode != "core_stopping" ||
		child.Status != "interrupted" || child.FailureCode != "core_stopping" ||
		len(parent.Executions) != 1 || len(child.Executions) != 1 ||
		parent.Executions[0].Kind != "sub_automation" || parent.Executions[0].Phase != "interrupted" ||
		parent.Executions[0].Evidence.ChildRunID != childRun || parent.Executions[0].Evidence.ReasonCode != "core_stopping" ||
		child.Executions[0].Kind != "delay" || child.Executions[0].Phase != "interrupted" {
		t.Fatalf("nested interruption lost or resumed: parent=%+v child=%+v", parent, child)
	}
	if count := conditionsCommandCount(t.Context(), t, second.address, source.powerEntityID); count != 0 {
		t.Fatalf("shutdown or restart dispatched %d later Commands", count)
	}
	reusableAssertOnlyRun(t, client, second, parentID, parentRun)
	reusableAssertOnlyRun(t, client, second, childID, childRun)
}
