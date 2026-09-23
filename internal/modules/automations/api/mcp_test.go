package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func connectAutomationMCP(t *testing.T, service automationsapi.Automations) *mcp.ClientSession {
	t.Helper()
	return connectAutomationMCPServer(
		t, mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"}), service,
	)
}

func connectAutomationMCPWithLogs(
	t *testing.T,
	service automationsapi.Automations,
	logs *bytes.Buffer,
) *mcp.ClientSession {
	t.Helper()
	return connectAutomationMCPServer(t, mcpapi.New(mcpapi.Config{
		Name: "hearth", Version: "1.0.0", Logger: slog.New(slog.NewJSONHandler(logs, nil)),
	}), service)
}

func connectAutomationMCPServer(
	t *testing.T,
	server *mcpapi.Server,
	service automationsapi.Automations,
) *mcp.ClientSession {
	t.Helper()
	automationsapi.RegisterMCP(server, service)
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

// TestAutomationMCPExposesExactToolNames proves the eight spec tool names list
// over the official client and the two automation resource templates publish.
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

	templates, err := session.ListResourceTemplates(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	published := make(map[string]bool, len(templates.ResourceTemplates))
	for _, template := range templates.ResourceTemplates {
		published[template.URITemplate] = true
	}
	for _, uriTemplate := range []string{
		"hearth://automation/{automation_id}",
		"hearth://automation/{automation_id}/history{?cursor,limit}",
	} {
		if !published[uriTemplate] {
			t.Fatalf("resource template %q is not published: %#v", uriTemplate, published)
		}
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

	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 2))},
	))
	if !strings.HasPrefix(created.ID, "aut_") || created.Revision != 1 {
		t.Fatalf("created automation = %#v", created)
	}
	if created.Definition.Name != "Office light" || len(created.Definition.Steps) != 2 {
		t.Fatalf("created definition = %#v", created.Definition)
	}

	read := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "get_automation", map[string]any{"automation_id": created.ID},
	))
	if !reflect.DeepEqual(read, created) {
		t.Fatalf("get_automation = %#v, want %#v", read, created)
	}

	page := decodeStructuredInto[automationsapi.AutomationCollectionBody](t, callAutomationTool(
		t, session, "list_automations", map[string]any{"limit": 1},
	))
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("list_automations = %#v", page)
	}

	replaced := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "replace_automation", map[string]any{
			"automation_id":     created.ID,
			"expected_revision": created.Revision,
			"definition":        definitionArguments(t, definitionDocument(t, 1)),
		},
	))
	if replaced.Revision != 2 || len(replaced.Definition.Steps) != 1 {
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

// TestAutomationMCPDeleteOutputPublishesDeletedRevision proves delete_automation
// names the revision it deleted deleted_revision in both its live body and its
// published output schema, instead of the ambiguous revision a caller could read
// as the revision the deletion produced.
func TestAutomationMCPDeleteOutputPublishesDeletedRevision(t *testing.T) {
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
	properties := outputSchemaProperties(t, "delete_automation", published.OutputSchema)
	for _, member := range []string{"automation_id", "deleted_revision", "deleted"} {
		if _, present := properties[member]; !present {
			t.Fatalf("delete_automation output schema has no %q member: %#v", member, properties)
		}
	}
	if _, present := properties["revision"]; present {
		t.Fatalf("delete_automation output schema still publishes revision: %#v", properties)
	}

	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
	))
	replaced := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
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

	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
	))

	run := decodeStructuredInto[automationsapi.AutomationRunBody](t, callAutomationTool(
		t, session, "start_automation_run", map[string]any{"automation_id": created.ID},
	))
	if run.AutomationID != created.ID || run.Source != "manual" {
		t.Fatalf("start_automation_run = %#v", run)
	}
	waitForAPI(t, service, created.ID, run.ID)

	history := decodeStructuredInto[automationsapi.AutomationHistoryCollectionBody](t, callAutomationTool(
		t, session, "list_automation_history", map[string]any{"automation_id": created.ID, "limit": 1},
	))
	if len(history.Items) != 1 || history.Items[0].ID != run.ID || history.Items[0].Status != "succeeded" {
		t.Fatalf("list_automation_history = %#v", history)
	}

	entry := decodeStructuredInto[automationsapi.AutomationHistoryEntryBody](t, callAutomationTool(
		t, session, "get_automation_history_entry",
		map[string]any{"automation_id": created.ID, "entry_id": run.ID},
	))
	if entry.Kind != "run" || entry.Run == nil || entry.Run.Status != "succeeded" {
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
	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
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

	entry := decodeStructuredInto[automationsapi.AutomationHistoryEntryBody](t, callAutomationTool(
		t, session, "get_automation_history_entry",
		map[string]any{"automation_id": created.ID, "entry_id": failure.HistoryID},
	))
	if entry.Kind != "skip" || entry.Skip == nil || entry.Skip.Reason != "conditions_false" {
		t.Fatalf("history entry = %#v", entry)
	}
	decision := entry.Skip.ConditionDecision
	if decision.Evaluation == nil || decision.Evaluation.Result != "false" || decision.Mode != "evaluated" {
		t.Fatalf("skip decision = %#v", decision)
	}

	history := decodeStructuredInto[automationsapi.AutomationHistoryCollectionBody](t, callAutomationTool(
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

	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
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

// schemaInvalidDefinitionDocuments returns definition documents the canonical
// Automation schema rejects, keyed by the constraint each violates; the cases
// span the Trigger, Condition, and Step subtrees.
func schemaInvalidDefinitionDocuments() map[string]string {
	const (
		observationTrigger = `{"id":"t","kind":"observation","entity_id":"e","dispositions":["applied"]}`
		step               = `{"id":"s","entity_id":"e","operation":"set","parameters":{}}`
	)
	return map[string]string{
		"unknown root member": `{"name":"x","enabled":true,"triggers":[` + observationTrigger +
			`],"steps":[` + step + `],"unexpected":true}`,
		"unknown Step member": `{"name":"x","enabled":true,"triggers":[` + observationTrigger +
			`],"steps":[{"id":"s","entity_id":"e","operation":"set","parameters":{},"unexpected":true}]}`,
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

// structurallyInvalidDefinitionDocuments returns definitions the canonical JSON
// Schema accepts but the strict structural decoder rejects.
func structurallyInvalidDefinitionDocuments() map[string]string {
	const (
		observationTrigger = `{"id":"t","kind":"observation","entity_id":"e","dispositions":["applied"]}`
		step               = `{"id":"s","entity_id":"e","operation":"set","parameters":{}}`
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
// definition number a float64 cannot represent reaches the service unchanged,
// for both create_automation and replace_automation.
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
// create_automation and replace_automation and asserts the service received it
// verbatim both times.
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
	parameters := string(steps[0].Parameters)
	if !strings.Contains(parameters, literal) {
		t.Fatalf("%s recorded parameters = %s, want the exact literal %s", tool, parameters, literal)
	}
	if strings.Contains(parameters, "9.007199254740992e+15") {
		t.Fatalf("%s recorded parameters = %s, want no float64 rounding", tool, parameters)
	}
}

// TestAutomationMCPPublishesTypedOutputSchemas proves all eight tools advertise a
// derived object output schema that names the body members an agent branches on.
func TestAutomationMCPPublishesTypedOutputSchemas(t *testing.T) {
	t.Parallel()
	session := connectAutomationMCP(t, newRecordingAutomations())

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]string{
		"create_automation":            "definition",
		"list_automations":             "items",
		"get_automation":               "definition",
		"replace_automation":           "definition",
		"delete_automation":            "deleted",
		"start_automation_run":         "snapshot",
		"list_automation_history":      "items",
		"get_automation_history_entry": "kind",
	}
	for _, tool := range tools.Tools {
		member, expected := want[tool.Name]
		if !expected {
			continue
		}
		delete(want, tool.Name)
		if tool.OutputSchema == nil {
			t.Fatalf("%s publishes no output schema", tool.Name)
		}
		properties := outputSchemaProperties(t, tool.Name, tool.OutputSchema)
		if _, present := properties[member]; !present {
			t.Fatalf("%s output schema has no %q member: %#v", tool.Name, member, properties)
		}
	}
	if len(want) != 0 {
		t.Fatalf("tools with no asserted output schema: %#v", want)
	}
}

// TestAutomationMCPDefinitionOutputSchemaDescribesTheDocument proves the
// definition-bearing tools publish the nested Step and Trigger members rather
// than an unconstrained object.
func TestAutomationMCPDefinitionOutputSchemaDescribesTheDocument(t *testing.T) {
	t.Parallel()
	session := connectAutomationMCP(t, newRecordingAutomations())

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "create_automation" && tool.Name != "get_automation" {
			continue
		}
		properties := outputSchemaProperties(t, tool.Name, tool.OutputSchema)
		definition, ok := properties["definition"].(map[string]any)
		if !ok {
			t.Fatalf("%s definition schema = %#v", tool.Name, properties["definition"])
		}
		members, _ := definition["properties"].(map[string]any)
		for _, member := range []string{"name", "enabled", "triggers", "steps"} {
			if _, present := members[member]; !present {
				t.Fatalf("%s definition schema has no %q member: %#v", tool.Name, member, members)
			}
		}
		steps, ok := members["steps"].(map[string]any)
		if !ok || !schemaAllowsType(steps, "array") {
			t.Fatalf("%s steps schema = %#v, want an array", tool.Name, members["steps"])
		}
	}
}

func outputSchemaProperties(t *testing.T, tool string, schema any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("%s output schema marshal: %v", tool, err)
	}
	var decoded struct {
		Type       string           `json:"type"`
		Properties map[string]any   `json:"properties"`
		AnyOf      []map[string]any `json:"anyOf"`
	}
	if unmarshalErr := json.Unmarshal(encoded, &decoded); unmarshalErr != nil {
		t.Fatalf("%s output schema decode %s: %v", tool, encoded, unmarshalErr)
	}
	properties := make(map[string]any)
	if decoded.Type == "object" {
		maps.Copy(properties, decoded.Properties)
	}
	for _, branch := range decoded.AnyOf {
		if branch["type"] != "object" {
			continue
		}
		branchProperties, ok := branch["properties"].(map[string]any)
		if !ok {
			continue
		}
		maps.Copy(properties, branchProperties)
	}
	if len(properties) == 0 {
		t.Fatalf("%s output schema declares no object members: %s", tool, encoded)
	}
	return properties
}

// schemaAllowsType reports whether one decoded JSON Schema node permits the
// named type directly or through a portable anyOf branch.
func schemaAllowsType(node map[string]any, expected string) bool {
	if name, ok := node["type"].(string); ok && name == expected {
		return true
	}
	branches, ok := node["anyOf"].([]any)
	if !ok {
		return false
	}
	for _, branch := range branches {
		object, objectOK := branch.(map[string]any)
		if objectOK && schemaAllowsType(object, expected) {
			return true
		}
	}
	return false
}

// schemaRejectsEveryValue recognizes the portable always-false schema used for
// closed-object additionalProperties constraints.
func schemaRejectsEveryValue(value any) bool {
	node, ok := value.(map[string]any)
	if !ok || node["type"] != "null" {
		return false
	}
	notNode, ok := node["not"].(map[string]any)
	return ok && notNode["type"] == "null"
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
	assertStepInputConstraints(t, name, schemaObject(t, name+" steps", properties["steps"]))
}

// assertTriggerInputConstraints checks the closed Trigger branches and their family fields.
func assertTriggerInputConstraints(t *testing.T, name string, triggers map[string]any) {
	t.Helper()
	items := schemaObject(t, name+" triggers items", triggers["items"])
	branches := schemaArray(t, name+" triggers oneOf", items["oneOf"])
	observation, entityEvent, heldState := false, false, false
	for _, branch := range branches {
		branchProperties := schemaObject(
			t, name+" trigger branch properties",
			schemaObject(t, name+" trigger branch", branch)["properties"],
		)
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
			forSeconds := schemaObject(t, name+" for_seconds", branchProperties["for_seconds"])
			if forSeconds["minimum"] != float64(1) || forSeconds["maximum"] != float64(2592000) {
				t.Fatalf("%s held duration schema = %#v", name, forSeconds)
			}
			kind := schemaObject(t, name+" held kind", branchProperties["kind"])
			if kind["const"] != "held_state" {
				t.Fatalf("%s held kind schema = %#v", name, kind)
			}
		}
	}
	if !observation || !entityEvent || !heldState || len(branches) != 3 {
		t.Fatalf("%s triggers schema branches = %#v, want Observation, Entity Event, and held state", name, branches)
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
func assertStepInputConstraints(t *testing.T, name string, steps map[string]any) {
	t.Helper()
	items := schemaObject(t, name+" steps items", steps["items"])
	if value, present := items["additionalProperties"]; !present || !schemaRejectsEveryValue(value) {
		t.Fatalf(
			"%s steps items additionalProperties = %#v, want an always-false schema",
			name, items["additionalProperties"],
		)
	}
	required := schemaStringSet(t, name+" steps required", items["required"])
	for _, member := range []string{"id", "entity_id", "operation", "parameters"} {
		if !required[member] {
			t.Fatalf("%s Step schema does not require %q: %#v", name, member, required)
		}
	}
	stepProperties := schemaObject(t, name+" steps properties", items["properties"])
	operation := schemaObject(t, name+" step operation", stepProperties["operation"])
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
}

func newRecordingAutomations() *recordingAutomations {
	return &recordingAutomations{}
}

func (stub *recordingAutomations) CreateAutomation(
	_ context.Context,
	definition automations.Definition,
) (automations.Record, error) {
	stub.callCount++
	stub.creations = append(stub.creations, definition)
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
	return automations.Record{ID: id, Revision: 1}, nil
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
	return automations.Run{
		ID:                automations.RunID("arn_00000000-0000-7000-8000-000000000000"),
		AutomationID:      input.AutomationID,
		AutomationName:    "stub",
		Revision:          1,
		Source:            automations.RunSourceManual,
		ConditionDecision: automations.NotConfiguredDecision(),
		Status:            automations.RunRunning,
		StartedAt:         time.Now().UTC(),
	}, nil
}

func (stub *recordingAutomations) GetHistoryEntry(
	_ context.Context,
	id automations.AutomationID,
	entryID string,
) (automations.HistoryEntry, error) {
	stub.callCount++
	run := automations.Run{
		ID:                automations.RunID(entryID),
		AutomationID:      id,
		ConditionDecision: automations.NotConfiguredDecision(),
		Status:            automations.RunSucceeded,
	}
	return automations.HistoryEntry{Kind: automations.HistoryRun, Run: &run}, nil
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
