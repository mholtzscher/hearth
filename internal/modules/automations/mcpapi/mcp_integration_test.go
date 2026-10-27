//go:build integration

package mcpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func connectAutomationMCP(t *testing.T, service automationsapi.Automations) *mcp.ClientSession {
	t.Helper()
	return connectAutomationMCPServer(
		t, mcptools.New(mcptools.Config{Name: "hearth", Version: "1.0.0"}), service,
	)
}

func connectAutomationMCPWithLogs(
	t *testing.T,
	service automationsapi.Automations,
	logs *bytes.Buffer,
) *mcp.ClientSession {
	t.Helper()
	return connectAutomationMCPServer(t, mcptools.New(mcptools.Config{
		Name: "hearth", Version: "1.0.0", Logger: slog.New(slog.NewJSONHandler(logs, nil)),
	}), service)
}

func connectAutomationMCPServer(
	t *testing.T,
	server *mcptools.Server,
	service automationsapi.Automations,
) *mcp.ClientSession {
	t.Helper()
	automationsapi.Register(server, service)
	httpServer := httptest.NewServer(server.HTTPHandler())
	t.Cleanup(httpServer.Close)
	session, err := mcp.NewClient(
		&mcp.Implementation{Name: "hearth-automations-test", Version: "1.0.0"},
		nil,
	).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Errorf("close MCP session: %v", closeErr)
		}
	})
	return session
}

// TestAutomationMCPExposesExactToolNames proves the eight spec tool names list
// over the official client.
func TestAutomationMCPExposesExactToolNames(t *testing.T) {
	t.Parallel()
	session := connectAutomationMCP(t, newRecordingAutomations())

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	want := []string{
		"create_automation",
		"list_automations",
		"get_automation",
		"replace_automation",
		"delete_automation",
		"start_automation_run",
		"list_automation_history",
		"get_automation_history_entry",
	}
	for _, name := range want {
		if !names[name] {
			t.Fatalf("tool %q is not registered: %#v", name, names)
		}
		delete(names, name)
	}
	if len(names) != 0 {
		t.Fatalf("unexpected tools registered: %#v", names)
	}
}

// This test protects the agent-facing comparison guidance and fails if the MCP
// catalog again exposes pointer as an unexplained string.
func TestAutomationMCPExplainsObservationComparisonPointers(t *testing.T) {
	t.Parallel()
	session := connectAutomationMCP(t, newRecordingAutomations())
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "create_automation" && tool.Name != "replace_automation" {
			continue
		}
		schema, marshalErr := json.Marshal(tool.InputSchema)
		if marshalErr != nil {
			t.Fatalf("marshal %s input schema: %v", tool.Name, marshalErr)
		}
		for _, guidance := range []string{"Observation value itself", "scalar values", "/state/value"} {
			if !strings.Contains(tool.Description+string(schema), guidance) {
				t.Fatalf("%s does not publish %q pointer guidance", tool.Name, guidance)
			}
		}
	}
}

// TestAutomationMCPDefinitionLifecycle proves create, list, get, replace, and
// delete call the Automations service over a live MCP session.
func TestAutomationMCPDefinitionLifecycle(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	created := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 2))},
	))
	if !strings.HasPrefix(created.ID, "aut_") || created.Revision != 1 {
		t.Fatalf("created automation = %#v", created)
	}

	read := decodeStructuredInto[AutomationDetailRead](t, callAutomationTool(
		t, session, "get_automation", map[string]any{"automation_id": created.ID},
	))
	if read.ID != created.ID || read.Revision != created.Revision || read.Definition.Name != "Office light" ||
		len(read.Definition.Steps) != 2 {
		t.Fatalf("get_automation = %#v, want %#v", read, created)
	}

	page := decodeStructuredInto[AutomationCollectionRead](t, callAutomationTool(
		t, session, "list_automations", map[string]any{"limit": 1},
	))
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("list_automations = %#v", page)
	}

	replaced := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "replace_automation", map[string]any{
			"automation_id":     created.ID,
			"expected_revision": created.Revision,
			"definition":        definitionArguments(t, definitionDocument(t, 1)),
		},
	))
	if replaced.Revision != 2 {
		t.Fatalf("replace_automation = %#v", replaced)
	}

	deleted := decodeStructuredInto[struct {
		AutomationID    string `json:"automation_id"`
		DeletedRevision int64  `json:"deleted_revision"`
		Deleted         bool   `json:"deleted"`
	}](t, callAutomationTool(t, session, "delete_automation", map[string]any{
		"automation_id":     created.ID,
		"expected_revision": replaced.Revision,
	}))
	if !deleted.Deleted || deleted.AutomationID != created.ID || deleted.DeletedRevision != 2 {
		t.Fatalf("delete_automation = %#v", deleted)
	}

	missing := callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": created.ID})
	if text := toolErrorText(t, missing); !strings.Contains(text, "not_found") {
		t.Fatalf("read-after-delete error = %q, want not_found", text)
	}
}

// Delete names the deleted revision, not a revision produced by deletion.
func TestAutomationMCPDeleteOutputCarriesDeletedRevision(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var published *mcp.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "delete_automation" {
			published = tool
		}
	}
	if published == nil {
		t.Fatal("delete_automation is not registered")
	}
	if published.OutputSchema != nil {
		t.Fatal("delete advertises output schema")
	}

	created := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
	))
	replaced := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "replace_automation", map[string]any{
			"automation_id":     created.ID,
			"expected_revision": created.Revision,
			"definition":        definitionArguments(t, definitionDocument(t, 1)),
		},
	))
	deleted := decodeStructuredInto[map[string]any](t, callAutomationTool(
		t, session, "delete_automation", map[string]any{
			"automation_id":     created.ID,
			"expected_revision": replaced.Revision,
		},
	))
	if deleted["deleted_revision"] != float64(replaced.Revision) {
		t.Fatalf("delete_automation deleted_revision = %#v, want %d",
			deleted["deleted_revision"], replaced.Revision)
	}
	if _, present := deleted["revision"]; present {
		t.Fatalf("delete_automation body still carries revision: %#v", deleted)
	}
}

// TestAutomationMCPManualRunAndHistory proves a manual Run admitted over MCP is
// readable through the history tool and the history entry tool.
func TestAutomationMCPManualRunAndHistory(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	created := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
	))

	run := decodeStructuredInto[RunReceiptRead](t, callAutomationTool(
		t, session, "start_automation_run", map[string]any{"automation_id": created.ID},
	))
	if run.AutomationID != created.ID || run.Status != "running" {
		t.Fatalf("start_automation_run = %#v", run)
	}
	waitForAPI(t, service, created.ID, run.ID)

	history := decodeStructuredInto[AutomationHistoryCollectionRead](t, callAutomationTool(
		t, session, "list_automation_history", map[string]any{"automation_id": created.ID, "limit": 1},
	))
	if len(history.Items) != 1 || history.Items[0].ID != run.ID || history.Items[0].Status != "succeeded" {
		t.Fatalf("list_automation_history = %#v", history)
	}

	entry := decodeStructuredInto[HistoryDetailRead](t, callAutomationTool(
		t, session, "get_automation_history_entry",
		map[string]any{"automation_id": created.ID, "entry_id": run.ID},
	))
	if entry.Kind != "run" || entry.Status != "succeeded" || entry.Cause.Kind != "manual" ||
		len(entry.Executions) != 1 {
		t.Fatalf("get_automation_history_entry = %#v", entry)
	}
}

// TestAutomationMCPManualConditionBlockRetainsReadableSkip proves one committed
// manual Skip crosses MCP twice: as a structured failure carrying its history
// reference, and as the history entry and history summary bodies the tools'
// output schemas describe.
func TestAutomationMCPManualConditionBlockRetainsReadableSkip(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	service := newAutomationService(t, stub)
	session := connectAutomationMCP(t, service)
	conditionEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	created := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "create_automation", map[string]any{
			"definition": definitionArguments(t, conditionalDefinitionDocument(t, conditionEntity)),
		},
	))
	stub.setEntityStateSnapshot(apiStateSnapshot(t, conditionEntity, `{"mode":"blocked"}`))

	blocked := callAutomationTool(t, session, "start_automation_run",
		map[string]any{"automation_id": created.ID})
	failure := decodeStructuredInto[struct {
		FailureCode string `json:"failure_code"`
		Message     string `json:"message"`
		HistoryID   string `json:"history_id"`
	}](t, blocked)
	if failure.FailureCode != "conditions_false" || failure.HistoryID == "" {
		t.Fatalf("blocked manual run failure = %#v", failure)
	}

	entry := decodeStructuredInto[HistoryDetailRead](t, callAutomationTool(
		t, session, "get_automation_history_entry",
		map[string]any{"automation_id": created.ID, "entry_id": failure.HistoryID},
	))
	if entry.Kind != "skip" || entry.Reason != "conditions_false" {
		t.Fatalf("history entry = %#v", entry)
	}
	decision := entry.ConditionDecision
	if decision.Evaluation == nil || decision.Evaluation.Result != "false" || decision.Mode != "evaluated" {
		t.Fatalf("skip decision = %#v", decision)
	}

	history := decodeStructuredInto[AutomationHistoryCollectionRead](t, callAutomationTool(
		t, session, "list_automation_history", map[string]any{"automation_id": created.ID},
	))
	if len(history.Items) != 1 || history.Items[0].Kind != "skip" {
		t.Fatalf("history = %#v", history)
	}
	if history.Items[0].Reason != "conditions_false" || history.Items[0].Status != "" {
		t.Fatalf("skip summary = %#v", history.Items[0])
	}
}

// TestAutomationMCPFailuresPreserveStableCodes proves domain failures cross as
// isError tool results carrying the stable problem code, and that a missing
// Automation and a missing history entry publish distinct not-found codes an
// agent can branch on.
func TestAutomationMCPFailuresPreserveStableCodes(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	created := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
	))
	unknown, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	unknownEntry, err := automations.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		tool      string
		arguments map[string]any
		code      string
	}{
		{
			"get_automation with an unknown Automation",
			"get_automation",
			map[string]any{"automation_id": string(unknown)},
			"automation_not_found",
		},
		{
			"replace_automation with an unknown Automation",
			"replace_automation",
			map[string]any{
				"automation_id":     string(unknown),
				"expected_revision": 1,
				"definition":        definitionArguments(t, definitionDocument(t, 1)),
			},
			"automation_not_found",
		},
		{
			"delete_automation with an unknown Automation",
			"delete_automation",
			map[string]any{"automation_id": string(unknown), "expected_revision": 1},
			"automation_not_found",
		},
		{
			"start_automation_run with an unknown Automation",
			"start_automation_run",
			map[string]any{"automation_id": string(unknown)},
			"automation_not_found",
		},
		{
			"get_automation_history_entry with an unknown entry",
			"get_automation_history_entry",
			map[string]any{"automation_id": created.ID, "entry_id": string(unknownEntry)},
			"automation_history_entry_not_found",
		},
		{
			"get_automation_history_entry with an unknown Automation",
			"get_automation_history_entry",
			map[string]any{"automation_id": string(unknown), "entry_id": string(unknownEntry)},
			"automation_history_entry_not_found",
		},
		{
			"replace_automation with a stale revision",
			"replace_automation",
			map[string]any{
				"automation_id":     created.ID,
				"expected_revision": 99,
				"definition":        definitionArguments(t, definitionDocument(t, 1)),
			},
			"revision_conflict",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := callAutomationTool(t, session, test.tool, test.arguments)
			fields := decodeStructuredInto[struct {
				FailureCode string `json:"failure_code"`
				Message     string `json:"message"`
			}](t, result)
			if fields.FailureCode != test.code {
				t.Fatalf("%s failure_code = %q, want %q", test.tool, fields.FailureCode, test.code)
			}
			if text := toolErrorText(t, result); !strings.HasPrefix(text, test.code+": ") {
				t.Fatalf("%s error text = %q, want the %q prefix", test.tool, text, test.code)
			}
		})
	}
}

// TestAutomationMCPRejectsMalformedScalarsWhileDecoding proves every malformed
// scalar an automation tool accepts is rejected while the SDK decodes and
// validates the arguments, so neither the handler nor the service sees it.
//
// A decoding failure crosses as a plain isError result with no structured
// content, which is how this test distinguishes it from a handler-produced
// ToolError. A ToolError always carries structured content. A malformed scalar
// the decode path rejects carries its stable failure code as the message prefix;
// a wrong JSON type is rejected by the advertised schema first, so it has no
// field-specific code and is asserted to be an SDK schema rejection instead.
func TestAutomationMCPRejectsMalformedScalarsWhileDecoding(t *testing.T) {
	t.Parallel()
	recorder := newRecordingAutomations()
	session := connectAutomationMCP(t, recorder)
	id := createdIDForInput(t)
	validDefinition := definitionArguments(t, definitionDocument(t, 1))
	cases := []struct {
		name      string
		tool      string
		arguments map[string]any
		code      string
	}{
		{
			"get_automation with a malformed ID", "get_automation",
			map[string]any{"automation_id": "not-an-id"}, "invalid_automation_id",
		},
		{
			"list_automations below the limit range", "list_automations",
			map[string]any{"limit": 0}, "invalid_limit",
		},
		{
			"list_automations above the limit range", "list_automations",
			map[string]any{"limit": 201}, "invalid_limit",
		},
		{
			"list_automation_history below the limit range", "list_automation_history",
			map[string]any{"automation_id": id, "limit": 0}, "invalid_limit",
		},
		{
			"replace_automation below the revision range", "replace_automation",
			map[string]any{"automation_id": id, "expected_revision": 0, "definition": validDefinition},
			"invalid_revision",
		},
		{
			"delete_automation below the revision range", "delete_automation",
			map[string]any{"automation_id": id, "expected_revision": 0}, "invalid_revision",
		},
		{
			"get_automation_history_entry with a malformed entry ID", "get_automation_history_entry",
			map[string]any{"automation_id": id, "entry_id": "not-an-id"}, "invalid_entry_id",
		},
		{
			"start_automation_run with a malformed ID", "start_automation_run",
			map[string]any{"automation_id": "not-an-id"}, "invalid_automation_id",
		},
		// A wrong JSON type never reaches the Go decoder: the advertised schema,
		// which derives a string for every ID and an integer for every boundary,
		// rejects it first. These carry no field-specific code and must still never
		// reach the service.
		{
			"get_automation with a non-string ID", "get_automation",
			map[string]any{"automation_id": 7}, "",
		},
		{
			"get_automation_history_entry with a non-string entry ID", "get_automation_history_entry",
			map[string]any{"automation_id": id, "entry_id": 7}, "",
		},
		{
			"list_automations with a non-integer limit", "list_automations",
			map[string]any{"limit": "lots"}, "",
		},
		{
			"delete_automation with a non-integer revision", "delete_automation",
			map[string]any{"automation_id": id, "expected_revision": "one"}, "",
		},
		{
			"delete_automation without the required revision", "delete_automation",
			map[string]any{"automation_id": id}, "",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := callAutomationTool(t, session, test.tool, test.arguments)
			text := toolErrorText(t, result)
			if test.code != "" {
				if !strings.HasPrefix(text, test.code+": ") {
					t.Fatalf("%s error text = %q, want the %q failure code", test.tool, text, test.code)
				}
			} else if !strings.Contains(text, "validating") {
				t.Fatalf("%s error text = %q, want an SDK schema rejection", test.tool, text)
			}
			if result.StructuredContent != nil {
				t.Fatalf(
					"%s structured content = %#v, want none: the failure must be an SDK decoding rejection",
					test.tool, result.StructuredContent,
				)
			}
		})
	}

	if recorder.callCount != 0 {
		t.Fatalf("malformed scalar reached the service %d times", recorder.callCount)
	}
}

// TestAutomationMCPRejectsSchemaInvalidDefinitionBeforeHandler proves the two
// definition-bearing tools reject a definition the canonical schema forbids
// while the SDK validates the arguments, before any handler runs.
//
// Each case violates a Trigger, Condition, or Step constraint inside definition,
// covering the canonical subtree a derived map schema cannot express. A
// validation rejection crosses as an isError result with no structured content,
// which distinguishes it from a handler-produced ToolError.
func TestAutomationMCPRejectsSchemaInvalidDefinitionBeforeHandler(t *testing.T) {
	t.Parallel()
	automationID := createdIDForInput(t)
	for name, document := range schemaInvalidDefinitionDocuments() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			recorder := newRecordingAutomations()
			session := connectAutomationMCP(t, recorder)
			calls := []struct {
				tool      string
				arguments json.RawMessage
			}{
				{
					tool:      "create_automation",
					arguments: json.RawMessage(`{"definition":` + document + `}`),
				},
				{
					tool: "replace_automation",
					arguments: json.RawMessage(
						`{"automation_id":"` + automationID + `","expected_revision":1,"definition":` + document + `}`,
					),
				},
			}
			for _, call := range calls {
				result := callAutomationTool(t, session, call.tool, call.arguments)
				if text := toolErrorText(t, result); !strings.Contains(text, "validating") {
					t.Fatalf("%s error text = %q, want an SDK schema rejection", call.tool, text)
				}
				if result.StructuredContent != nil {
					t.Fatalf(
						"%s structured content = %#v, want none: the rejection must precede the handler",
						call.tool, result.StructuredContent,
					)
				}
			}
			if recorder.callCount != 0 {
				t.Fatalf("schema-invalid definition reached the service %d times", recorder.callCount)
			}
		})
	}
}

// TestAutomationMCPMapsStructurallyInvalidDefinitionToToolError proves the
// canonical schema is a pre-filter, not a replacement for the strict decoder: a
// definition the JSON Schema cannot reject — a whitespace-only name or a repeated
// Trigger or Step ID — still fails in the handler, crosses as a structured
// isError ToolError, and never reaches the service.
func TestAutomationMCPMapsStructurallyInvalidDefinitionToToolError(t *testing.T) {
	t.Parallel()
	for name, document := range structurallyInvalidDefinitionDocuments() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			recorder := newRecordingAutomations()
			session := connectAutomationMCP(t, recorder)
			result := callAutomationTool(t, session, "create_automation",
				json.RawMessage(`{"definition":`+document+`}`))
			if text := toolErrorText(t, result); !strings.Contains(text, "invalid_automation") {
				t.Fatalf("create_automation error text = %q, want invalid_automation", text)
			}
			if result.StructuredContent == nil {
				t.Fatal("rejected definition has no structured failure content")
			}
			if recorder.callCount != 0 {
				t.Fatalf("structurally invalid definition reached the service %d times", recorder.callCount)
			}
		})
	}
}

// TestAutomationMCPMissingRequiredArgumentNeverReachesService proves the SDK
// input schema rejects an omitted required argument before the handler runs.
func TestAutomationMCPMissingRequiredArgumentNeverReachesService(t *testing.T) {
	t.Parallel()
	recorder := newRecordingAutomations()
	session := connectAutomationMCP(t, recorder)

	result := callAutomationTool(t, session, "get_automation", map[string]any{})
	if !result.IsError {
		t.Fatalf("result = %#v, want an isError tool result", result)
	}
	if recorder.callCount != 0 {
		t.Fatalf("missing argument reached the service %d times", recorder.callCount)
	}
}

// TestAutomationMCPDefinitionNumbersAboveFloat64PrecisionStayExact proves a
// definition number a float64 cannot represent reaches the service and returns
// unchanged, for both create_automation and replace_automation.
//
// 9007199254740993 is 2^53+1, the first integer a float64 cannot hold, and
// 18446744073709551615 is [math.MaxUint64]; a definition that survives this
// assertion cannot have passed through a map[string]any decode. The argument
// object is sent as raw JSON because a decoded map would already round the
// literal before the server ever saw it.
func TestAutomationMCPDefinitionNumbersAboveFloat64PrecisionStayExact(t *testing.T) {
	t.Parallel()
	for _, literal := range []string{"9007199254740993", "18446744073709551615"} {
		t.Run(literal, func(t *testing.T) {
			t.Parallel()
			assertDefinitionNumberSurvivesCreateAndReplace(t, literal)
		})
	}
}

// assertDefinitionNumberSurvivesCreateAndReplace drives one literal through
// create_automation and replace_automation and asserts the service and client
// receive it verbatim both times.
func assertDefinitionNumberSurvivesCreateAndReplace(t *testing.T, literal string) {
	t.Helper()
	recorder := newRecordingAutomations()
	session := connectAutomationMCP(t, recorder)
	document := precisionDefinitionDocument(t, literal)

	createResult := callAutomationTool(t, session, "create_automation",
		json.RawMessage(`{"definition":`+document+`}`))
	if createResult.IsError {
		t.Fatalf("create_automation error = %q", toolErrorText(t, createResult))
	}
	assertExactStepParameters(t, "create_automation", recorder.creations, literal)

	replaceResult := callAutomationTool(t, session, "replace_automation",
		json.RawMessage(`{"automation_id":"`+createdIDForInput(t)+
			`","expected_revision":1,"definition":`+document+`}`))
	if replaceResult.IsError {
		t.Fatalf("replace_automation error = %q", toolErrorText(t, replaceResult))
	}
	if len(recorder.replacements) != 1 {
		t.Fatalf("replacements = %#v, want one", recorder.replacements)
	}
	assertExactStepParameters(t, "replace_automation",
		[]automations.Definition{recorder.replacements[0].definition}, literal)
	for _, result := range []*mcp.CallToolResult{createResult, replaceResult} {
		body := exactToolBody(t, result)
		if len(body) != 2 || body["automation_id"] == nil || body["revision"] == nil {
			t.Fatalf("write receipt = %v", body)
		}
	}
}

// All Automation tools retain complete inputs without advertising outputs.
func TestAutomationMCPOmitsOutputSchemas(t *testing.T) {
	t.Parallel()
	session := connectAutomationMCP(t, newRecordingAutomations())

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]string{
		"create_automation":            "automation_id",
		"list_automations":             "items",
		"get_automation":               "definition",
		"replace_automation":           "automation_id",
		"delete_automation":            "deleted",
		"start_automation_run":         "run_id",
		"list_automation_history":      "items",
		"get_automation_history_entry": "kind",
	}
	for _, tool := range tools.Tools {
		_, expected := want[tool.Name]
		if !expected {
			continue
		}
		delete(want, tool.Name)
		if tool.OutputSchema != nil || tool.InputSchema == nil {
			t.Fatalf("invalid discovery: %+v", tool)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %#v", want)
	}
}

// Detail preserves the complete canonical definition rather than a summary.
func TestAutomationMCPDefinitionDetailPreservesTheDocument(t *testing.T) {
	t.Parallel()
	session := connectAutomationMCP(t, newAutomationService(t, newAPIDevices()))
	want := definitionArguments(t, definitionDocument(t, 1))
	want["name"] = "Office light" // The service commits the normalized domain name.
	created := decodeStructuredInto[AutomationReceiptRead](
		t,
		callAutomationTool(t, session, "create_automation", map[string]any{"definition": want}),
	)
	result := callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": created.ID})
	detail := decodeStructuredInto[map[string]any](t, result)
	got, _ := json.Marshal(detail["definition"])
	expected, _ := json.Marshal(want)
	if result.IsError || string(got) != string(expected) {
		t.Fatalf("definition = %s, want %s", got, expected)
	}
}

// TestAutomationMCPDefinitionInputSchemaExposesStrictConstraints proves the two
// definition-bearing tools advertise the canonical Trigger, Condition, and Step
// constraints inside definition, so tools/list tells an agent the accepted
// document shape and the SDK enforces the same schema before any handler runs.
func TestAutomationMCPDefinitionInputSchemaExposesStrictConstraints(t *testing.T) {
	t.Parallel()
	session := connectAutomationMCP(t, newRecordingAutomations())

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	published := make(map[string]*mcp.Tool, len(tools.Tools))
	for _, tool := range tools.Tools {
		published[tool.Name] = tool
	}
	for _, name := range []string{"create_automation", "replace_automation"} {
		tool, registered := published[name]
		if !registered {
			t.Fatalf("tool %q is not registered", name)
		}
		assertDefinitionInputSchema(t, name, tool)
	}
}

// TestAutomationMCPForwardsManualBypass proves start_automation_run translates
// its flat argument into the domain ManualRunInput.
func TestAutomationMCPForwardsManualBypass(t *testing.T) {
	t.Parallel()
	recorder := newRecordingAutomations()
	session := connectAutomationMCP(t, recorder)
	id := createdIDForInput(t)

	callAutomationTool(t, session, "start_automation_run", map[string]any{
		"automation_id": id, "bypass_conditions": true,
	})
	if len(recorder.manualRuns) != 1 {
		t.Fatalf("manual runs = %#v, want one", recorder.manualRuns)
	}
	want := automations.ManualRunInput{AutomationID: automations.AutomationID(id), BypassConditions: true}
	if recorder.manualRuns[0] != want {
		t.Fatalf("manual run input = %#v, want %#v", recorder.manualRuns[0], want)
	}
}

// TestAutomationMCPForwardsReplacement proves replace_automation translates the
// flat envelope into the domain revision and definition.
func TestAutomationMCPForwardsReplacement(t *testing.T) {
	t.Parallel()
	recorder := newRecordingAutomations()
	session := connectAutomationMCP(t, recorder)
	id := createdIDForInput(t)

	callAutomationTool(t, session, "replace_automation", map[string]any{
		"automation_id":     id,
		"expected_revision": 7,
		"definition":        definitionArguments(t, definitionDocument(t, 1)),
	})
	if len(recorder.replacements) != 1 {
		t.Fatalf("replacements = %#v, want one", recorder.replacements)
	}
	replacement := recorder.replacements[0]
	if string(replacement.id) != id || replacement.revision != 7 {
		t.Fatalf("replacement = %#v", replacement)
	}
	if replacement.definition.Name != "Office light" || len(replacement.definition.Steps) != 1 {
		t.Fatalf("replacement definition = %#v", replacement.definition)
	}
}

// TestAutomationMCPToolLogsInternalCauseWithoutLeakingIt proves a production
// internal failure on an Automation tool emits one structured server diagnostic
// with safe metadata while the raw service cause stays out of both halves.
func TestAutomationMCPToolLogsInternalCauseWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	const cause = "sqlite: database is locked by /var/lib/hearth/hearth.db"
	service := newRecordingAutomations()
	service.getErr = errors.New(cause)
	var logs bytes.Buffer
	session := connectAutomationMCPWithLogs(t, service, &logs)

	result := callAutomationTool(t, session, "get_automation", map[string]any{
		"automation_id": createdIDForInput(t),
	})
	if text := toolErrorText(t, result); text != "internal_error: internal error" {
		t.Fatalf("error text = %q, want the generic message", text)
	}
	if result.StructuredContent == nil {
		t.Fatal("structured content = nil, want the failure fields")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if strings.Contains(string(raw), cause) {
		t.Fatalf("structured content leaked the cause: %s", raw)
	}
	var fields map[string]any
	if unmarshalErr := json.Unmarshal(raw, &fields); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	if fields["failure_code"] != "internal_error" {
		t.Fatalf("failure_code = %#v, want internal_error", fields["failure_code"])
	}

	record := automationMCPLogRecord(t, logs.Bytes())
	if record["level"] != "ERROR" || record["event"] != "mcp.tool_internal_failure" ||
		record["tool"] != "get_automation" {
		t.Fatalf("log record = %#v", record)
	}
	if record["error_code"] != "internal_error" {
		t.Fatalf("log error_code = %#v, want internal_error", record["error_code"])
	}
	if errorType, _ := record["error_type"].(string); errorType == "" {
		t.Fatalf("log error_type = %#v, want a non-empty Go error type name", record["error_type"])
	}
	if _, ok := record["error"]; ok {
		t.Fatalf("log record = %#v, want no raw error field", record)
	}
	if strings.Contains(logs.String(), cause) {
		t.Fatalf("logs leaked the cause: %s", logs.Bytes())
	}
}
