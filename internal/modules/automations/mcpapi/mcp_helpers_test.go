package mcpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func automationMCPLogRecord(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	line, _, _ := bytes.Cut(bytes.TrimSpace(raw), []byte("\n"))
	if len(line) == 0 {
		t.Fatal("no log record was written")
	}
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatalf("decode log record %s: %v", line, err)
	}
	return record
}

// callAutomationTool invokes one tool and fails on transport errors. arguments is
// any because a test that must prove byte-exact JSON sends the whole argument
// object as a [json.RawMessage] instead of a decoded map.
func callAutomationTool(
	t *testing.T,
	session *mcp.ClientSession,
	name string,
	arguments any,
) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func decodeStructuredInto[T any](t *testing.T, result *mcp.CallToolResult) T {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var value T
	if unmarshalErr := json.Unmarshal(raw, &value); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	return value
}

func toolErrorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("result = %#v, want an isError tool result", result)
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatalf("error result has no text content: %#v", result.Content)
	return ""
}

func definitionArguments(t *testing.T, document string) map[string]any {
	t.Helper()
	var definition map[string]any
	if err := json.Unmarshal([]byte(document), &definition); err != nil {
		t.Fatalf("decode definition document: %v", err)
	}
	return definition
}

// schemaInvalidDefinitionDocuments returns definition documents the canonical
// Automation schema rejects, keyed by the constraint each violates; the cases
// span the Trigger, Condition, and Step subtrees.
func schemaInvalidDefinitionDocuments() map[string]string {
	const (
		observationTrigger = `{"id":"t","kind":"observation","entity_id":"e","dispositions":["applied"]}`
		step               = `{"kind":"command", "id":"s","entity_id":"e","operation":"set","parameters":{}}`
	)
	return map[string]string{
		"unknown root member": `{"name":"x","enabled":true,"triggers":[` + observationTrigger +
			`],"steps":[` + step + `],"unexpected":true}`,
		"unknown Step member": `{"name":"x","enabled":true,"triggers":[` + observationTrigger +
			`],"steps":[{"kind":"command", "id":"s","entity_id":"e","operation":"set","parameters":{},"unexpected":true}]}`,
		"Trigger kind outside the enumeration": `{"name":"x","enabled":true,"triggers":[` +
			`{"id":"t","kind":"bogus","entity_id":"e","dispositions":["applied"]}],"steps":[` + step + `]}`,
		"Trigger disposition outside the enumeration": `{"name":"x","enabled":true,"triggers":[` +
			`{"id":"t","kind":"observation","entity_id":"e","dispositions":["bogus"]}],"steps":[` + step + `]}`,
		"missing required Steps": `{"name":"x","enabled":true,"triggers":[` + observationTrigger + `]}`,
		"empty Steps list": `{"name":"x","enabled":true,"triggers":[` + observationTrigger +
			`],"steps":[]}`,
		"Condition with an empty child list": `{"name":"x","enabled":true,"conditions":` +
			`{"id":"c","kind":"all","children":[]},"triggers":[` + observationTrigger +
			`],"steps":[` + step + `]}`,
		"Condition with an unknown member": `{"name":"x","enabled":true,"conditions":` +
			`{"id":"c","kind":"entity_state","entity_id":"e","value_pointer":"/x","operator":"eq",` +
			`"operand":1,"unexpected":true},"triggers":[` + observationTrigger + `],"steps":[` + step + `]}`,
	}
}

// structurallyInvalidDefinitionDocuments returns definitions the canonical JSON
// Schema accepts but the strict structural decoder rejects.
func structurallyInvalidDefinitionDocuments() map[string]string {
	const (
		observationTrigger = `{"id":"t","kind":"observation","entity_id":"e","dispositions":["applied"]}`
		step               = `{"kind":"command", "id":"s","entity_id":"e","operation":"set","parameters":{}}`
	)
	return map[string]string{
		"whitespace-only name": `{"name":"   ","enabled":true,"triggers":[` + observationTrigger +
			`],"steps":[` + step + `]}`,
		"duplicate Trigger IDs": `{"name":"x","enabled":true,"triggers":[` + observationTrigger +
			`,` + observationTrigger + `],"steps":[` + step + `]}`,
		"duplicate Step IDs": `{"name":"x","enabled":true,"triggers":[` + observationTrigger +
			`],"steps":[` + step + `,` + step + `]}`,
	}
}

// assertExactStepParameters fails unless the single recorded definition's whole
// Step parameter document still carries literal as its exact JSON number.
func assertExactStepParameters(
	t *testing.T,
	tool string,
	definitions []automations.Definition,
	literal string,
) {
	t.Helper()
	if len(definitions) != 1 {
		t.Fatalf("%s recorded definitions = %#v, want one", tool, definitions)
	}
	steps := definitions[0].Steps
	if len(steps) != 1 {
		t.Fatalf("%s recorded steps = %#v, want one", tool, steps)
	}
	parameters := string(steps[0].Body.(automations.CommandStep).Parameters)
	if !strings.Contains(parameters, literal) {
		t.Fatalf("%s recorded parameters = %s, want the exact literal %s", tool, parameters, literal)
	}
	if strings.Contains(parameters, "9.007199254740992e+15") {
		t.Fatalf("%s recorded parameters = %s, want no float64 rounding", tool, parameters)
	}
}

func compilePublishedSchema(t *testing.T, document any, location string) *jsonschema.Schema {
	t.Helper()
	document, location = publishedSchemaResource(document, location)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	uri, _, _ := strings.Cut(location, "#")
	if err = compiler.AddResource(uri, decoded); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

// OpenAPI's components keyword is not a JSON Schema applicator. Enter the
// selected component's resource base as an OpenAPI-aware resolver would.
func publishedSchemaResource(document any, location string) (any, string) {
	_, fragment, _ := strings.Cut(location, "#")
	target, component := strings.CutPrefix(fragment, "/components/schemas/")
	if !component {
		return document, location
	}
	name, pointer, nested := strings.Cut(target, "/")
	schemas := document.(map[string]any)["components"].(map[string]any)["schemas"].(map[string]any)
	resource := schemas[name].(map[string]any)
	id, hasID := resource["$id"].(string)
	if !hasID {
		return document, location
	}
	if nested {
		id += "#/" + pointer
	}
	return resource, id
}

// schemaRejectsEveryValue recognizes the portable always-false schema used for
// closed-object additionalProperties constraints.
func schemaRejectsEveryValue(value any) bool {
	if value == false {
		return true
	}
	node, ok := value.(map[string]any)
	if !ok || node["type"] != "null" {
		return false
	}
	notNode, ok := node["not"].(map[string]any)
	return ok && notNode["type"] == "null"
}

// assertDefinitionInputSchema checks one definition-bearing tool advertises the
// canonical definition document constraints.
func assertDefinitionInputSchema(t *testing.T, name string, tool *mcp.Tool) {
	t.Helper()
	definition := inputSchemaDefinition(t, name, tool)

	if value, present := definition["additionalProperties"]; !present || !schemaRejectsEveryValue(value) {
		t.Fatalf(
			"%s definition schema additionalProperties = %#v, want an always-false schema",
			name, definition["additionalProperties"],
		)
	}
	required := schemaStringSet(t, name+" definition required", definition["required"])
	for _, member := range []string{"name", "enabled", "triggers", "steps"} {
		if !required[member] {
			t.Fatalf("%s definition schema does not require %q: %#v", name, member, required)
		}
	}
	properties := schemaObject(t, name+" definition properties", definition["properties"])
	for _, member := range []string{"name", "enabled", "triggers", "conditions", "steps"} {
		if _, present := properties[member]; !present {
			t.Fatalf("%s definition schema declares no %q member: %#v", name, member, properties)
		}
	}

	assertTriggerInputConstraints(t, name, schemaObject(t, name+" triggers", properties["triggers"]))
	assertConditionInputConstraints(t, name, definition)
	assertStepInputConstraints(t, name, definition)
}

// assertTriggerInputConstraints checks the closed Trigger branches and their family fields.
func assertTriggerInputConstraints(t *testing.T, name string, triggers map[string]any) {
	t.Helper()
	items := schemaObject(t, name+" triggers items", triggers["items"])
	branches := schemaArray(t, name+" triggers oneOf", items["oneOf"])
	observation, entityEvent, heldState, cron := false, false, false, false
	for _, branch := range branches {
		object := schemaObject(t, name+" trigger branch", branch)
		branchProperties := schemaObject(
			t, name+" trigger branch properties",
			schemaObject(t, name+" trigger branch", branch)["properties"],
		)
		required := schemaStringSet(t, name+" trigger required", object["required"])
		if _, present := branchProperties["expression"]; present {
			cron = true
			assertCronSchema(t, name, object)
		} else if !required["entity_id"] {
			t.Fatalf("%s device Trigger does not require entity_id: %#v", name, object)
		}
		if _, present := branchProperties["dispositions"]; present {
			observation = true
			dispositions := schemaObject(t, name+" dispositions", branchProperties["dispositions"])
			enum := schemaStringSet(
				t, name+" dispositions enum",
				schemaObject(t, name+" dispositions items", dispositions["items"])["enum"],
			)
			if !enum["applied"] || !enum["unchanged"] {
				t.Fatalf("%s disposition enum = %#v, want applied and unchanged", name, enum)
			}
		}
		if _, present := branchProperties["event_name"]; present {
			entityEvent = true
		}
		if _, present := branchProperties["for_seconds"]; present {
			heldState = true
			assertHeldStateInputConstraints(t, name, branchProperties)
		}
	}
	if !observation || !entityEvent || !heldState || !cron || len(branches) != 4 {
		t.Fatalf(
			"%s triggers schema branches = %#v, want Observation, Entity Event, held state, and cron",
			name,
			branches,
		)
	}
}

func assertHeldStateInputConstraints(t *testing.T, name string, properties map[string]any) {
	t.Helper()
	forSeconds := schemaObject(t, name+" for_seconds", properties["for_seconds"])
	if forSeconds["minimum"] != float64(1) || forSeconds["maximum"] != float64(2592000) {
		t.Fatalf("%s held duration schema = %#v", name, forSeconds)
	}
	kind := schemaObject(t, name+" held kind", properties["kind"])
	if kind["const"] != "held_state" {
		t.Fatalf("%s held kind schema = %#v", name, kind)
	}
}

// assertConditionInputConstraints checks the Conditions member references the
// recursive canonical condition subtree the definition carries.
func assertConditionInputConstraints(t *testing.T, name string, definition map[string]any) {
	t.Helper()
	properties := schemaObject(t, name+" definition properties", definition["properties"])
	conditions := schemaObject(t, name+" conditions", properties["conditions"])
	if ref, _ := conditions["$ref"].(string); !strings.HasSuffix(ref, "/$defs/condition") {
		t.Fatalf("%s conditions schema = %#v, want a $ref to the canonical condition", name, conditions)
	}
	condition := schemaObject(
		t, name+" condition definition",
		schemaObject(t, name+" definition $defs", definition["$defs"])["condition"],
	)
	branches := schemaArray(t, name+" condition oneOf", condition["oneOf"])
	if len(branches) != 4 {
		t.Fatalf("%s condition oneOf has %d branches, want the four canonical kinds", name, len(branches))
	}
}

// assertStepInputConstraints checks the Steps member advertises the closed Step
// object and its required action members.
func assertStepInputConstraints(t *testing.T, name string, definition map[string]any) {
	t.Helper()
	defs := schemaObject(t, name+" definitions", definition["$defs"])
	sequence := schemaObject(t, name+" step sequence", defs["stepSequence"])
	if sequence["minItems"] != float64(1) || sequence["maxItems"] != float64(32) {
		t.Fatalf("%s sequence bounds = %v", name, sequence)
	}
	step := schemaObject(t, name+" recursive step", defs["step"])
	if len(schemaArray(t, name+" step alternatives", step["oneOf"])) != 6 {
		t.Fatalf("%s lacks command/if/choose/delay/notification/function alternatives: %v", name, step)
	}
	items := schemaObject(t, name+" command step", defs["commandStep"])
	if value, present := items["additionalProperties"]; !present || !schemaRejectsEveryValue(value) {
		t.Fatalf(
			"%s steps items additionalProperties = %#v, want an always-false schema",
			name, items["additionalProperties"],
		)
	}
	required := schemaStringSet(t, name+" steps required", items["required"])
	for _, member := range []string{"id", "kind", "entity_id", "operation", "parameters"} {
		if !required[member] {
			t.Fatalf("%s Step schema does not require %q: %#v", name, member, required)
		}
	}
	stepProperties := schemaObject(t, name+" steps properties", items["properties"])
	operation := schemaObject(t, name+" step operation", stepProperties["operation"])
	if ref, ok := operation["$ref"].(string); ok {
		operation = schemaObject(t, name+" operation identifier", defs[strings.TrimPrefix(ref, "#/$defs/")])
	}
	if pattern, _ := operation["pattern"].(string); pattern == "" {
		t.Fatalf("%s Step operation schema = %#v, want a pattern", name, operation)
	}
}

func inputSchemaDefinition(t *testing.T, name string, tool *mcp.Tool) map[string]any {
	t.Helper()
	input := schemaObject(t, name+" input schema", tool.InputSchema)
	properties := schemaObject(t, name+" input properties", input["properties"])
	return schemaObject(t, name+" definition schema", properties["definition"])
}

func schemaObject(t *testing.T, label string, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON object schema", label, value)
	}
	return object
}

func schemaArray(t *testing.T, label string, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON array", label, value)
	}
	return array
}

func schemaStringSet(t *testing.T, label string, value any) map[string]bool {
	t.Helper()
	set := make(map[string]bool)
	for _, member := range schemaArray(t, label, value) {
		name, ok := member.(string)
		if !ok {
			t.Fatalf("%s member = %#v, want a string", label, member)
		}
		set[name] = true
	}
	return set
}

// precisionDefinitionDocument renders one strict definition whose single Step
// parameter document carries literal as its exact JSON number.
func precisionDefinitionDocument(t *testing.T, literal string) string {
	t.Helper()
	document := definitionDocument(t, 1)
	const placeholder = `{"value":true}`
	if !strings.Contains(document, placeholder) {
		t.Fatalf("definition document has no %s placeholder", placeholder)
	}
	return strings.Replace(document, placeholder, `{"level":`+literal+`}`, 1)
}

func createdIDForInput(t *testing.T) string {
	t.Helper()
	id, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

// replacementCall records one ReplaceAutomation service invocation.
type replacementCall struct {
	id         automations.AutomationID
	revision   int64
	definition automations.Definition
}

// recordingAutomations is an [automationsapi.Automations] seam that records what
// the MCP handlers pass through, returning scripted minimal values so handler
// output mapping is exercised without persistence.
type recordingAutomations struct {
	callCount      int
	creations      []automations.Definition
	manualRuns     []automations.ManualRunInput
	replacements   []replacementCall
	historyQueries []automations.ListHistoryParams
	// getErr and historyErr script a non-not-found read failure.
	getErr     error
	historyErr error
	saveErr    error
}

func newRecordingAutomations() *recordingAutomations {
	return &recordingAutomations{}
}

func recordingDefinition() automations.Definition {
	return automations.Definition{
		Name: "stub", Enabled: true,
		Triggers: []automations.Trigger{{ID: "schedule", Body: automations.CronTrigger{Expression: "* * * * *"}}},
		Steps: []automations.Step{{ID: "command", Body: automations.CommandStep{
			EntityID:      "ent_00000000-0000-7000-8000-000000000000",
			OperationName: "set",
			Parameters:    devices.CommandParameters(`{"value":true}`),
		}}},
	}
}

func (stub *recordingAutomations) CreateAutomation(
	_ context.Context,
	definition automations.Definition,
) (automations.Record, error) {
	stub.callCount++
	stub.creations = append(stub.creations, definition)
	if stub.saveErr != nil {
		return automations.Record{}, stub.saveErr
	}
	return automations.Record{
		ID:         automations.AutomationID("aut_00000000-0000-7000-8000-000000000000"),
		Revision:   1,
		Definition: definition,
	}, nil
}

func (stub *recordingAutomations) GetAutomation(
	_ context.Context,
	id automations.AutomationID,
) (automations.Record, error) {
	stub.callCount++
	if stub.getErr != nil {
		return automations.Record{}, stub.getErr
	}
	return automations.Record{ID: id, Revision: 1, Definition: recordingDefinition()}, nil
}

func (stub *recordingAutomations) ListAutomations(
	_ context.Context,
	_ automations.ListAutomationsParams,
) (automations.Page[automations.Record], error) {
	stub.callCount++
	return automations.Page[automations.Record]{}, nil
}

func (stub *recordingAutomations) ReplaceAutomation(
	_ context.Context,
	id automations.AutomationID,
	expectedRevision int64,
	definition automations.Definition,
) (automations.Record, error) {
	stub.callCount++
	stub.replacements = append(stub.replacements, replacementCall{
		id: id, revision: expectedRevision, definition: definition,
	})
	if stub.saveErr != nil {
		return automations.Record{}, stub.saveErr
	}
	return automations.Record{ID: id, Revision: expectedRevision + 1, Definition: definition}, nil
}

func (stub *recordingAutomations) DeleteAutomation(
	_ context.Context,
	_ automations.AutomationID,
	_ int64,
) error {
	stub.callCount++
	return nil
}

func (stub *recordingAutomations) StartManualRun(
	_ context.Context,
	input automations.ManualRunInput,
) (automations.Run, error) {
	stub.callCount++
	stub.manualRuns = append(stub.manualRuns, input)
	return automations.NewRunSnapshot(
		automations.Record{ID: input.AutomationID, Revision: 1, Definition: recordingDefinition()},
		"arn_00000000-0000-7000-8000-000000000000", automations.ManualCause{}, nil,
		automations.NotConfiguredDecision(), time.Now().UTC(),
	), nil
}

func (stub *recordingAutomations) GetHistoryEntry(
	_ context.Context,
	id automations.AutomationID,
	entryID string,
) (automations.HistoryEntry, error) {
	stub.callCount++
	started := time.Now().UTC()
	run := automations.NewRunSnapshot(
		automations.Record{ID: id, Revision: 1, Definition: recordingDefinition()},
		automations.RunID(entryID), automations.ManualCause{}, nil,
		automations.NotConfiguredDecision(), started,
	)
	run.State = automations.CompletedRun{CompletedAt: started.Add(time.Second), Outcome: automations.SucceededRun{}}
	commandID := devices.CommandID("cmd_00000000-0000-7000-8000-000000000000")
	reservation := automations.CommandReservation{
		CommandID:     commandID,
		CorrelationID: devices.CorrelationID("cor_00000000-0000-7000-8000-000000000000"),
	}
	completedAt := started.Add(time.Second)
	run.Executions = []automations.StepExecution{{
		StepID: "command", Sequence: 0,
		Details: automations.CommandExecutionDetails{
			StartedAt: started, CompletedAt: &completedAt,
			Reservation: &reservation, Outcome: automations.SatisfiedCommand{VerifiedCommandID: commandID},
		},
	}}
	return run, nil
}

func (stub *recordingAutomations) ListHistory(
	_ context.Context,
	params automations.ListHistoryParams,
) (automations.Page[automations.HistorySummary], error) {
	stub.callCount++
	stub.historyQueries = append(stub.historyQueries, params)
	if stub.historyErr != nil {
		return automations.Page[automations.HistorySummary]{}, stub.historyErr
	}
	return automations.Page[automations.HistorySummary]{}, nil
}

var _ automationsapi.Automations = (*recordingAutomations)(nil)

func assertCronSchema(t *testing.T, name string, branch map[string]any) {
	t.Helper()
	if !schemaRejectsEveryValue(branch["additionalProperties"]) {
		t.Fatalf("%s cron permits unknown fields: %v", name, branch)
	}
	properties := schemaObject(t, name+" cron properties", branch["properties"])
	required := schemaStringSet(t, name+" cron required", branch["required"])
	if len(properties) != 3 || len(required) != 3 || !required["id"] || !required["kind"] || !required["expression"] {
		t.Fatalf("%s cron fields = %v, required = %v", name, properties, required)
	}
	if schemaObject(t, name+" cron kind", properties["kind"])["const"] != "cron" {
		t.Fatalf("%s cron discriminator = %v", name, properties["kind"])
	}
	expression := schemaObject(t, name+" expression", properties["expression"])
	if expression["type"] != "string" || expression["minLength"] != float64(1) ||
		expression["maxLength"] != float64(512) {
		t.Fatalf("%s expression constraints = %v", name, expression)
	}
	description, _ := expression["description"].(string)
	for _, restriction := range []string{"five-field", "literal *", "512 UTF-8 bytes", "seconds", "descriptors", "timezone prefixes", "extensions"} {
		if !strings.Contains(description, restriction) {
			t.Errorf("%s expression description lacks %q: %s", name, restriction, description)
		}
	}
	if len(schemaArray(t, name+" expression examples", expression["examples"])) == 0 {
		t.Errorf("%s expression has no examples", name)
	}
}
