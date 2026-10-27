package mcpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// conditionalDefinitionDocument renders one valid strict definition whose single
// entity_state Condition requires /mode to equal "allowed" within five minutes.
// The string evidence keeps failure assertions free of incidental digits.
func conditionalDefinitionDocument(t *testing.T, conditionEntity devices.EntityID) string {
	t.Helper()
	triggerEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	actionEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{
		"name": "Conditioned",
		"enabled": true,
		"triggers": [
			{"id":"trigger","kind":"observation","entity_id":%q,"dispositions":["applied"]}
		],
		"conditions": {
			"id":"mode-allowed","kind":"entity_state","entity_id":%q,"value_pointer":"/mode",
			"operator":"eq","operand":"allowed","max_age_seconds":300
		},
		"steps": [{"kind":"command", "id":"step_0","entity_id":%q,"operation":"set","parameters":{"literal":{"value":true}}}]
	}`, string(triggerEntity), string(conditionEntity), string(actionEntity))
}

// apiStateSnapshot covers one Entity with fresh accepted State carrying the
// supplied JSON value, so manual Condition admission evaluates real evidence.
func apiStateSnapshot(t *testing.T, entityID devices.EntityID, value string) devices.EntityStateSnapshot {
	t.Helper()
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return devices.EntityStateSnapshot{
		Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{
			entityID: {
				EntityID: entityID,
				Exists:   true,
				State: &devices.State{
					EntityID:      entityID,
					Value:         devices.Value(value),
					ObservationID: observationID,
					ObservedAt:    time.Now().UTC().Add(-time.Second),
				},
			},
		},
	}
}

const branchExactNumber = "9007199254740993"

func nestedBranchDocument(t *testing.T, entity devices.EntityID) string {
	t.Helper()
	command := fmt.Sprintf(
		`{"kind":"command", "id":"selected","entity_id":%q,"operation":"set","parameters":{"literal":{"value":%s}}}`,
		entity,
		branchExactNumber,
	)
	return fmt.Sprintf(
		`{"name":"Nested","enabled":true,"triggers":[{"id":"warm","kind":"cron","expression":"* * * * *"}],"steps":[
		{"id":"route","kind":"choose","branches":[
			{"id":"automatic","conditions":{"id":"matched","kind":"trigger","trigger_ids":["warm"]},"steps":[{"kind":"command", "id":"unselected","entity_id":%q,"operation":"set","parameters":{"literal":{}}}]},
			{"id":"manual","conditions":{"id":"not-matched","kind":"not","child":{"id":"matched","kind":"trigger","trigger_ids":["warm"]}},"steps":[
				{"id":"level","kind":"if","conditions":{"id":"exact","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":%s},"then":[%s]}]}
		]}]}`,
		entity,
		entity,
		branchExactNumber,
		command,
	)
}

// Decode the protocol's JSON text, not the SDK client's float64 StructuredContent.
func exactToolBody(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatal(toolErrorText(t, result))
	}
	if len(result.Content) != 1 {
		t.Fatalf("unexpected result content: %v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("result content is %T", result.Content[0])
	}
	return exactJSONObject(t, text.Text)
}

func exactJSONObject(t *testing.T, document string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewBufferString(document))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		t.Fatal(err)
	}
	delete(object, "$schema")
	return object
}

func assertNestedExactDefinition(t *testing.T, body map[string]any) {
	t.Helper()
	definition := body["definition"].(map[string]any)
	route := definition["steps"].([]any)[0].(map[string]any)
	branches := route["branches"].([]any)
	level := branches[1].(map[string]any)["steps"].([]any)[0].(map[string]any)
	condition := level["conditions"].(map[string]any)
	command := level["then"].([]any)[0].(map[string]any)
	parameters := command["parameters"].(map[string]any)["literal"].(map[string]any)
	if condition["operand"] != json.Number(branchExactNumber) || parameters["value"] != json.Number(branchExactNumber) {
		t.Fatalf("rounded operand/parameters: %v / %v", condition, parameters)
	}
	if command["kind"] != "command" {
		t.Fatalf("command kind = %v", command["kind"])
	}
	for _, field := range []string{"conditions", "then", "branches"} {
		if _, present := command[field]; present {
			t.Fatalf("command contains %s: %v", field, command)
		}
	}
	if _, present := level["else"]; present {
		t.Fatal("omitted else was emitted")
	}
	if _, present := route["default"]; present {
		t.Fatal("omitted default was emitted")
	}
	if _, present := route["entity_id"]; present {
		t.Fatal("branch contains command fields")
	}
}

func delayDocument(steps string) string {
	return `{"name":"Wait","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[` + steps + `]}`
}

func compileDelayContractSchema(t *testing.T, document any, fragment string) *jsonschema.Schema {
	t.Helper()
	return compilePublishedSchema(t, document, "https://hearth.test/contract"+fragment)
}

func delayTools(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func delaySemanticInvalidDocuments() []string {
	predicate := `{"id":"matched","kind":"trigger","trigger_ids":["tick"]}`
	deep := `{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`
	for index := range 8 {
		deep = fmt.Sprintf(`{"id":"if-%d","kind":"if","conditions":%s,"then":[%s]}`, index, predicate, deep)
	}
	var arms []string
	for arm := range 3 {
		var children []string
		for child := range 21 {
			children = append(
				children,
				fmt.Sprintf(`{"id":"wait-%d-%d","kind":"delay","duration_ms":{"literal":1}}`, arm, child),
			)
		}
		arms = append(
			arms,
			fmt.Sprintf(
				`{"id":"if-%d","kind":"if","conditions":%s,"then":[%s]}`,
				arm,
				predicate,
				strings.Join(children, ","),
			),
		)
	}
	return []string{delayDocument(deep), delayDocument(strings.Join(arms, ","))}
}

func assertCompletedDelayOnlyRun(t *testing.T, run map[string]any, stub *apiDevices) {
	t.Helper()
	if run["status"] != "succeeded" || stub.executionCount() != 0 {
		t.Fatalf("delay-only execution = %v, Commands = %d", run, stub.executionCount())
	}
	delays := mcpExecutionsOfKind(t, run["executions"].([]any), "delay")
	if len(delays) != 2 {
		t.Fatalf("reached delays = %v", delays)
	}
	for index, stepID := range []string{"nested-wait", "last-wait"} {
		assertDelayMetadata(t, delays[index], stepID, index+2, "succeeded")
	}
}

func mcpExecutionsOfKind(t *testing.T, executions []any, kind string) []map[string]any {
	t.Helper()
	var found []map[string]any
	for _, item := range executions {
		execution := item.(map[string]any)
		if execution["kind"] == kind {
			found = append(found, execution)
		}
	}
	return found
}

func assertDelayMetadata(t *testing.T, delay map[string]any, id string, sequence int, phase string) {
	t.Helper()
	if _, err := time.Parse(time.RFC3339Nano, delay["started_at"].(string)); err != nil {
		t.Fatal(err)
	}
	if delay["step_id"] != id || delay["sequence"] != json.Number(strconv.Itoa(sequence)) ||
		delay["kind"] != "delay" || delay["phase"] != phase {
		t.Fatalf("incorrect Delay execution: %v", delay)
	}
	evidence := delay["evidence"].(map[string]any)
	_, completed := delay["completed_at"]
	_, failed := evidence["reason_code"]
	if completed != (phase != "running") || failed != (phase == "interrupted") {
		t.Fatalf("terminal field presence = %v", delay)
	}
	fields := 6
	if completed {
		fields++
		if _, err := time.Parse(time.RFC3339Nano, delay["completed_at"].(string)); err != nil {
			t.Fatal(err)
		}
	}
	if len(delay) != fields || len(evidence) != boolInt(failed) {
		t.Fatalf("unexpected delay fields = %v", delay)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func waitForDelayEvidence(ctx context.Context, t *testing.T, service *automations.Service, id, runID string) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		entry, err := service.GetHistoryEntry(ctx, automations.AutomationID(id), runID)
		if err != nil {
			t.Fatal(err)
		}
		if run, ok := entry.(automations.Run); ok {
			for _, execution := range run.Executions {
				if _, isDelay := execution.Details.(automations.DelayExecutionDetails); isDelay {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("wait never started")
		case <-ticker.C:
		}
	}
}

func assertCronDefinition(t *testing.T, body map[string]any, expression string) {
	t.Helper()
	definition := pageObject(t, body["definition"])
	triggers := definition["triggers"].([]any)
	if len(triggers) != 2 {
		t.Fatalf("cron triggers = %v", triggers)
	}
	for index, item := range triggers {
		trigger := pageObject(t, item)
		want, wantID := expression, "morning"
		if index == 1 {
			want = "*/15 * * * *"
			wantID = "quarter"
		}
		if len(trigger) != 3 || trigger["id"] != wantID || trigger["kind"] != "cron" || trigger["expression"] != want {
			t.Fatalf("cron Trigger = %v, want exactly id, kind, expression %q", trigger, want)
		}
	}
}

func assertScheduleEvidenceAbsent(t *testing.T, body map[string]any) {
	t.Helper()
	cause, ok := body["cause"].(map[string]any)
	if !ok || cause["kind"] != "schedule" || len(cause) != 1 {
		t.Fatalf("schedule source = %v", body)
	}
	for _, field := range []string{"source", "fact", "held_state", "entity_id"} {
		if _, present := body[field]; present {
			t.Errorf("schedule history contains %s: %v", field, body)
		}
	}
}

func assertCronMCPValidationError(t *testing.T, result *mcp.CallToolResult, definition map[string]any) {
	t.Helper()
	text := toolErrorText(t, result)
	trigger := definition["triggers"].([]any)[0].(map[string]any)
	expression, isString := trigger["expression"].(string)
	structurallyValid := len(trigger) == 3 && trigger["kind"] == "cron" && isString &&
		utf8.RuneCountInString(expression) > 0 && utf8.RuneCountInString(expression) <= 512
	want := "validating"
	if structurallyValid {
		want = "invalid_automation"
	}
	if !result.IsError || !strings.Contains(text, want) {
		t.Fatalf("MCP error = %s, want %s", text, want)
	}
}

// canonicalJSON re-encodes one decoded JSON value with sorted object keys, so a
// route body and an MCP structured content that differ only in key order compare
// equal.
func canonicalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON value: %v", err)
	}
	return string(encoded)
}

// pageCursor returns one page's next_cursor, or "" when the page published none.
func pageCursor(t *testing.T, body any) string {
	t.Helper()
	cursor, _ := pageObject(t, body)["next_cursor"].(string)
	return cursor
}

func pageObject(t *testing.T, body any) map[string]any {
	t.Helper()
	object, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("page body = %#v, want a JSON object", body)
	}
	return object
}
