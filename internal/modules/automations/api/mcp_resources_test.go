package api_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
)

// readAutomationResource reads one resource URI and returns its JSON text.
func readAutomationResource(t *testing.T, session *mcp.ClientSession, uri string) string {
	t.Helper()
	result, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("read resource %s: %v", uri, err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text == "" {
		t.Fatalf("resource %s contents = %#v", uri, result.Contents)
	}
	return result.Contents[0].Text
}

// decodeResourceInto unmarshals one resource JSON text into a typed body.
func decodeResourceInto[T any](t *testing.T, text string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("decode resource body %s: %v", text, err)
	}
	return value
}

// TestAutomationMCPCollectionResourceMatchesListTool proves the bare
// hearth://automations collection reads the default first page through
// ListAutomations with the same body as the list tool, and that the template
// serves paged reads.
func TestAutomationMCPCollectionResourceMatchesListTool(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	first := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
	))
	second := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 2))},
	))

	resource := decodeResourceInto[automationsapi.AutomationCollectionBody](
		t, readAutomationResource(t, session, "hearth://automations"),
	)
	tool := decodeStructuredInto[automationsapi.AutomationCollectionBody](t, callAutomationTool(
		t, session, "list_automations", map[string]any{},
	))
	if len(resource.Items) != 2 || len(tool.Items) != 2 {
		t.Fatalf("resource items = %d, tool items = %d, want 2 and 2", len(resource.Items), len(tool.Items))
	}
	if resource.Items[0].ID != first.ID || resource.Items[1].ID != second.ID {
		t.Fatalf("resource order = [%s %s], want creation order [%s %s]",
			resource.Items[0].ID, resource.Items[1].ID, first.ID, second.ID)
	}
	if !reflect.DeepEqual(resource, tool) {
		t.Fatalf("resource = %#v, tool = %#v, want equal", resource, tool)
	}

	paged := decodeResourceInto[automationsapi.AutomationCollectionBody](
		t, readAutomationResource(t, session, "hearth://automations?limit=1"),
	)
	if len(paged.Items) != 1 || paged.Items[0].ID != first.ID || paged.NextCursor == nil {
		t.Fatalf("paged collection = %#v, want oldest %s with a cursor", paged, first.ID)
	}
}

// TestAutomationMCPCollectionIsListed proves resources/list carries the
// concrete automations collection and the template catalog carries its
// parameterized family.
func TestAutomationMCPCollectionIsListed(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	listed, err := session.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(listed.Resources) != 1 || listed.Resources[0].URI != "hearth://automations" {
		t.Fatalf("resources = %#v, want exactly hearth://automations", listed.Resources)
	}
	if listed.Resources[0].MIMEType != "application/json" {
		t.Fatalf("resource = %#v, want JSON contents", listed.Resources[0])
	}

	templates, err := session.ListResourceTemplates(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	found := false
	for _, template := range templates.ResourceTemplates {
		if template.URITemplate == "hearth://automations{?cursor,limit}" {
			found = true
		}
	}
	if !found || len(templates.ResourceTemplates) != 3 {
		t.Fatalf("templates = %#v, want 3 including the automations collection", templates.ResourceTemplates)
	}
}

// TestAutomationMCPCollectionResourceQueryContract proves the collection
// rejects unknown, repeated, and out-of-range parameters as invalid params.
func TestAutomationMCPCollectionResourceQueryContract(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	for _, uri := range []string{
		"hearth://automations?status=succeeded",
		"hearth://automations?limit=lots",
		"hearth://automations?limit=0",
		"hearth://automations?cursor=not-a-cursor",
	} {
		if _, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Fatalf("read %s succeeded, want an error", uri)
		} else if code := resourceErrorCode(t, err); code != jsonrpc.CodeInvalidParams {
			t.Fatalf("read %s error code = %d, want %d (invalid params)", uri, code, jsonrpc.CodeInvalidParams)
		}
	}
}

// TestAutomationMCPDefinitionResourceMatchesToolRead proves the definition
// resource reads through GetAutomation with the same body as the tool.
func TestAutomationMCPDefinitionResourceMatchesToolRead(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 2))},
	))
	resource := decodeResourceInto[automationsapi.AutomationBody](
		t, readAutomationResource(t, session, "hearth://automation/"+created.ID),
	)
	if !reflect.DeepEqual(resource, created) {
		t.Fatalf("automation resource = %#v, want %#v", resource, created)
	}
}

// TestAutomationMCPHistoryResourcePages proves the history resource honors the
// limit and cursor URI parameters and matches the history tool.
func TestAutomationMCPHistoryResourcePages(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
	))
	var runIDs []string
	for range 2 {
		run := decodeStructuredInto[automationsapi.AutomationRunBody](t, callAutomationTool(
			t, session, "start_automation_run", map[string]any{"automation_id": created.ID},
		))
		waitForAPI(t, service, created.ID, run.ID)
		runIDs = append(runIDs, run.ID)
	}

	first := decodeResourceInto[automationsapi.AutomationHistoryCollectionBody](
		t, readAutomationResource(t, session, "hearth://automation/"+created.ID+"/history?limit=1"),
	)
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first history page = %#v", first)
	}
	if first.Items[0].ID != runIDs[1] {
		t.Fatalf("first history item = %s, want newest %s", first.Items[0].ID, runIDs[1])
	}

	second := decodeResourceInto[automationsapi.AutomationHistoryCollectionBody](
		t, readAutomationResource(t, session,
			"hearth://automation/"+created.ID+"/history?limit=1&cursor="+*first.NextCursor),
	)
	if len(second.Items) != 1 || second.Items[0].ID != runIDs[0] {
		t.Fatalf("second history page = %#v, want %s", second, runIDs[0])
	}

	tool := decodeStructuredInto[automationsapi.AutomationHistoryCollectionBody](t, callAutomationTool(
		t, session, "list_automation_history", map[string]any{"automation_id": created.ID, "limit": 2},
	))
	if len(tool.Items) != 2 {
		t.Fatalf("history tool = %#v, want two items", tool)
	}
}

// TestAutomationMCPResourceRejectsUnknownAddress proves a malformed or unknown
// automation resource reads as not found rather than leaking a partial body.
func TestAutomationMCPResourceRejectsUnknownAddress(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	unknown, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}

	for _, uri := range []string{
		"hearth://automation/not-an-id",
		"hearth://automation/" + string(unknown),
		"hearth://automation/not-an-id/history",
		// The SDK refuses any URI a registered template does not match, so the
		// definition template, which declares no query, never reaches its reader
		// with a query parameter attached.
		"hearth://automation/" + string(unknown) + "?limit=1",
	} {
		if _, readErr := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri}); readErr == nil {
			t.Fatalf("resource %s read succeeded, want an error", uri)
		} else if !strings.Contains(readErr.Error(), "not found") {
			t.Fatalf("resource %s error = %v, want not found", uri, readErr)
		}
	}
}

// TestAutomationMCPResourceQueryContract proves the resource readers reject an
// unknown, repeated, or invalid query parameter as invalid params rather than
// silently ignoring it and returning a broader page than a client asked for.
func TestAutomationMCPResourceQueryContract(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
	))
	definition := "hearth://automation/" + created.ID
	history := definition + "/history"
	tests := []struct {
		name    string
		uri     string
		message string
	}{
		{"unsupported history parameter", history + "?status=succeeded", "unsupported query parameter"},
		{"repeated limit", history + "?limit=1&limit=2", "must appear once"},
		{"repeated cursor", history + "?cursor=one&cursor=two", "must appear once"},
		{"non-numeric limit", history + "?limit=lots", "limit must be an integer"},
		{"limit below the range", history + "?limit=0", "limit must be between 1 and 200"},
		{"limit above the range", history + "?limit=201", "limit must be between 1 and 200"},
		{"malformed cursor", history + "?cursor=not-a-cursor", "automation history cursor is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: test.uri})
			if err == nil {
				t.Fatalf("read %s succeeded, want an error", test.uri)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("read %s error = %q, want %q", test.uri, err.Error(), test.message)
			}
			if code := resourceErrorCode(t, err); code != jsonrpc.CodeInvalidParams {
				t.Fatalf(
					"read %s error code = %d, want %d (invalid params)",
					test.uri, code, jsonrpc.CodeInvalidParams,
				)
			}
		})
	}
}

// TestAutomationMCPResourceInternalFailuresStayGeneric proves a non-not-found
// service failure reads as a generic internal error and never leaks the
// underlying server detail.
func TestAutomationMCPResourceInternalFailuresStayGeneric(t *testing.T) {
	t.Parallel()
	const secret = "sqlite: database is locked by /var/lib/hearth/hearth.db"
	id := createdIDForInput(t)
	tests := []struct {
		name string
		uri  string
		stub func() *recordingAutomations
	}{
		{
			"definition read",
			"hearth://automation/" + id,
			func() *recordingAutomations {
				stub := newRecordingAutomations()
				stub.getErr = errors.New(secret)
				return stub
			},
		},
		{
			"history read",
			"hearth://automation/" + id + "/history",
			func() *recordingAutomations {
				stub := newRecordingAutomations()
				stub.historyErr = errors.New(secret)
				return stub
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := connectAutomationMCP(t, test.stub())
			_, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: test.uri})
			if err == nil {
				t.Fatalf("read %s succeeded, want an error", test.uri)
			}
			if !strings.Contains(err.Error(), "internal error") {
				t.Fatalf("read %s error = %q, want a generic internal error", test.uri, err.Error())
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "locked") {
				t.Fatalf("read %s leaked server detail: %q", test.uri, err.Error())
			}
			if code := resourceErrorCode(t, err); code != jsonrpc.CodeInternalError {
				t.Fatalf(
					"read %s error code = %d, want %d (internal error)",
					test.uri, code, jsonrpc.CodeInternalError,
				)
			}
		})
	}
}

// TestAutomationMCPDefinitionNumberSurvivesCreateAndResourceRead proves one
// definition number above 2^53 is unchanged end to end: create_automation sends
// it exactly, the repository stores it exactly, and the hearth:// definition
// resource returns it verbatim.
//
// The resource read is the path that proves client-visible parity, because a
// resource read returns its own bytes while the SDK re-marshals a typed tool
// result through float64 (see mcp_outputs.go).
func TestAutomationMCPDefinitionNumberSurvivesCreateAndResourceRead(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	const literal = "9007199254740993"

	created := decodeStructuredInto[automationsapi.AutomationBody](t, callAutomationTool(
		t, session, "create_automation",
		json.RawMessage(`{"definition":`+precisionDefinitionDocument(t, literal)+`}`),
	))
	text := readAutomationResource(t, session, "hearth://automation/"+created.ID)
	if !strings.Contains(text, literal) {
		t.Fatalf("definition resource = %s, want the exact literal %s", text, literal)
	}
	if strings.Contains(text, "9.007199254740992e+15") {
		t.Fatalf("definition resource = %s, want no float64 rounding", text)
	}
}

// resourceErrorCode returns the JSON-RPC code one resource read failure carries.
func resourceErrorCode(t *testing.T, err error) int64 {
	t.Helper()
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error %v is not a JSON-RPC error", err)
	}
	return rpcErr.Code
}
