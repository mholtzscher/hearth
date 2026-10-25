//go:build e2e

package hearthd //nolint:testpackage // Public journeys reuse the production Core fixture.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/httpapi"
	adaptersmcp "github.com/mholtzscher/hearth/internal/modules/adapters/mcpapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const automationFunctionAdapter = "automation-functions"

// These DTOs deliberately decode the public JSON rather than sharing production
// mappers: missing fields and lossy numeric conversions must be observable.
type automationFunctionDiscovery struct {
	CatalogRevision     string `json:"catalog_revision"`
	ContractFingerprint string `json:"contract_fingerprint"`
	Callable            bool   `json:"callable"`
}

type automationFunctionEvidence struct {
	StepID            string     `json:"step_id"`
	Position          int        `json:"position"`
	InvocationID      string     `json:"invocation_id"`
	Status            string     `json:"status"`
	StartedAt         time.Time  `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	CatalogRevision   string     `json:"catalog_revision"`
	InvocationOutcome string     `json:"invocation_outcome"`
	ReasonCode        string     `json:"reason_code"`
}

type automationFunctionRun struct {
	DataEvidence string                       `json:"data_evidence"`
	Data         []json.RawMessage            `json:"data"`
	Status       string                       `json:"status"`
	FailureCode  string                       `json:"failure_code"`
	Functions    []automationFunctionEvidence `json:"functions"`
	Snapshot     json.RawMessage              `json:"snapshot"`
	Cause        struct {
		Kind string `json:"kind"`
		Fact struct {
			Family   string `json:"family"`
			EntityID string `json:"entity_id"`
			EventID  string `json:"event_id"`
			Name     string `json:"name"`
		} `json:"fact"`
	} `json:"cause"`
}

func connectAutomationFunctionsMCP(t *testing.T, core *functionCoreRun) *mcp.ClientSession {
	t.Helper()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "automation-functions-journey", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: "http://" + core.address + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func automationFunctionsMCP(t *testing.T, client *mcp.ClientSession, name, arguments string) []byte {
	t.Helper()
	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(arguments)})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result == nil || result.IsError {
		t.Fatalf("%s: %+v", name, result)
	}
	// StructuredContent is decoded by the official client through float64. Text
	// contains the server's original JSON and is the exact-number public contract.
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && json.Valid([]byte(text.Text)) {
			return []byte(text.Text)
		}
	}
	t.Fatalf("%s returned no JSON text: %+v", name, result)
	return nil
}

func startAutomationFunctionSDK(
	t *testing.T,
	url string,
	timeout int,
	calls chan<- adapter.FunctionRequest,
) *adapter.Session {
	t.Helper()
	session, err := adapter.Connect(t.Context(), adapter.Config{
		AdapterID:       automationFunctionAdapter,
		SoftwareName:    "synthetic-automation-functions",
		SoftwareVersion: "1",
		NATSURL:         url,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	function := automationIntegrationFunction(
		func(_ context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
			calls <- request
			return adapter.FunctionSucceeded{Result: json.RawMessage(automationFunctionOutput)}
		},
	)
	function.Descriptor.TimeoutMS = timeout
	if _, err = session.StartFunctionServer(t.Context(), []adapter.Function{function}); err != nil {
		t.Fatal(err)
	}
	return session
}

func discoverAutomationFunction(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	mcpClient *mcp.ClientSession,
) automationFunctionDiscovery {
	t.Helper()
	path := "/v1/adapters/" + automationFunctionAdapter + "/functions"
	detail := decodeNotification[automationFunctionDiscovery](t,
		successfulFunctionCoreRequest(t, client, core, http.MethodGet, path+"/inspect", ""))
	if !detail.Callable || detail.CatalogRevision == "" ||
		!regexp.MustCompile(`^sha256-v1:[0-9a-f]{64}$`).MatchString(detail.ContractFingerprint) {
		t.Fatalf("discovery lacks callable contract: %+v", detail)
	}
	for _, raw := range [][]byte{
		successfulFunctionCoreRequest(t, client, core, http.MethodGet, path, ""),
		automationFunctionsMCP(t, mcpClient, "list_adapter_functions", `{"adapter_id":"`+automationFunctionAdapter+`"}`),
	} {
		page := decodeNotification[struct {
			Items []automationFunctionDiscovery `json:"items"`
		}](t, raw)
		if len(page.Items) != 1 || page.Items[0] != detail {
			t.Fatalf("list/detail disagree: %s, detail %+v", raw, detail)
		}
	}
	mcpDetail := decodeNotification[automationFunctionDiscovery](
		t,
		automationFunctionsMCP(
			t,
			mcpClient,
			"get_adapter_function",
			`{"adapter_id":"`+automationFunctionAdapter+`","function_key":"inspect"}`,
		),
	)
	if mcpDetail != detail {
		t.Fatalf("HTTP/MCP discovery disagree: %+v / %+v", detail, mcpDetail)
	}
	return detail
}

func automationFunctionDefinition(entity, fingerprint string) string {
	return fmt.Sprintf(
		`{"name":"Inspect precise values","enabled":true,"triggers":[{"id":"press","kind":"entity_event","entity_id":%q,"event_name":"single_press"}],"steps":[{"id":"inspect","kind":"function","adapter_id":%q,"function_key":"inspect","contract_fingerprint":%q,"arguments":{"literal":%s}}]}`,
		entity,
		automationFunctionAdapter,
		fingerprint,
		functionExactObject,
	)
}

func readAutomationFunctionRun(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	automationID, runID string,
) automationFunctionRun {
	t.Helper()
	raw := successfulFunctionCoreRequest(t, client, core, http.MethodGet,
		"/v1/automations/"+automationID+"/history/"+runID, "")
	entry := decodeNotification[struct {
		Kind string                `json:"kind"`
		Run  automationFunctionRun `json:"run"`
	}](t, raw)
	if entry.Kind != "run" || entry.Run.Functions == nil {
		t.Fatalf("not canonical Run detail with functions array: %s", raw)
	}
	return entry.Run
}

func awaitAutomationFunctionRun(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	automationID string,
	previous map[string]bool,
) string {
	t.Helper()
	var id string
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		raw := successfulFunctionCoreRequest(
			t,
			client,
			core,
			http.MethodGet,
			"/v1/automations/"+automationID+"/history",
			"",
		)
		page := decodeNotification[struct {
			Items []struct {
				ID     string `json:"id"`
				Kind   string `json:"kind"`
				Status string `json:"status"`
			} `json:"items"`
		}](t, raw)
		for _, item := range page.Items {
			if item.Kind == "run" && item.Status != "running" && !previous[item.ID] {
				id = item.ID
				return true, nil
			}
		}
		return false, nil
	})
	previous[id] = true
	return id
}

func assertAutomationFunctionSuccess(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	mcpClient *mcp.ClientSession,
	automationID, runID, revision, cause string,
	calls <-chan adapter.FunctionRequest,
) string {
	t.Helper()
	run := readAutomationFunctionRun(t, client, core, automationID, runID)
	if run.Status != "succeeded" || run.Cause.Kind != cause || len(run.Functions) != 1 {
		t.Fatalf("successful Function Run: %+v", run)
	}
	if run.DataEvidence != "recorded" || len(run.Data) != 3 {
		t.Fatalf("Function value journal: %+v", run)
	}
	captured := decodeNotification[struct {
		Kind   string          `json:"kind"`
		Output json.RawMessage `json:"output"`
	}](t, run.Data[2])
	if captured.Kind != "function_output" || string(captured.Output) != automationFunctionOutput {
		t.Fatalf("copied exact output: %s", run.Data[2])
	}
	evidence := run.Functions[0]
	if evidence.StepID != "inspect" || evidence.Position != 0 || evidence.Status != "succeeded" ||
		evidence.CatalogRevision != revision || evidence.InvocationOutcome != "succeeded" ||
		evidence.StartedAt.IsZero() || evidence.CompletedAt == nil || evidence.CompletedAt.Before(evidence.StartedAt) {
		t.Fatalf("Function success evidence: %+v", evidence)
	}
	select {
	case request := <-calls:
		if request.InvocationID != evidence.InvocationID || request.FunctionKey != "inspect" ||
			string(request.Arguments) != functionExactObject || request.Deadline.IsZero() {
			t.Fatalf("SDK payload does not match retained reservation: %+v / %+v", request, evidence)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("successful Run has no SDK request")
	}
	assertAutomationFunctionInvocation(t, client, core, mcpClient, evidence, revision)
	mcpRun := decodeNotification[automationFunctionRun](t, automationFunctionsMCP(t, mcpClient,
		"get_automation_history_entry", fmt.Sprintf(`{"automation_id":%q,"entry_id":%q}`, automationID, runID)))
	if mcpRun.Status != run.Status || len(mcpRun.Functions) != 1 || !reflect.DeepEqual(mcpRun.Functions[0], evidence) ||
		string(
			mcpRun.Snapshot,
		) != string(
			run.Snapshot,
		) || mcpRun.DataEvidence != run.DataEvidence || !reflect.DeepEqual(mcpRun.Data, run.Data) {
		t.Fatalf("HTTP/MCP canonical evidence differs: %+v / %+v", run, mcpRun)
	}
	return evidence.InvocationID
}

func assertAutomationFunctionInvocation(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	mcpClient *mcp.ClientSession,
	evidence automationFunctionEvidence,
	revision string,
) {
	t.Helper()
	raw := successfulFunctionCoreRequest(
		t,
		client,
		core,
		http.MethodGet,
		"/v1/adapter-function-invocations/"+evidence.InvocationID,
		"",
	)
	invocation := decodeNotification[adaptersapi.InvocationBody](t, raw)
	if invocation.ID == "" || invocation.Status != "succeeded" || invocation.CompletedAt == nil ||
		string(invocation.Arguments) != functionExactObject || string(invocation.Result) != automationFunctionOutput {
		t.Fatalf("exact Automation invocation: %+v", invocation)
	}
	if invocation.ID != evidence.InvocationID || invocation.CatalogRevision != revision ||
		invocation.AdapterID != automationFunctionAdapter {
		t.Fatalf("linked Invocation: %+v", invocation)
	}
	mcpInvocation := decodeNotification[adaptersmcp.InvocationDetail](
		t,
		automationFunctionsMCP(
			t,
			mcpClient,
			"get_adapter_function_invocation",
			fmt.Sprintf(`{"invocation_id":%q}`, evidence.InvocationID),
		),
	)
	if mcpInvocation.InvocationID != evidence.InvocationID || mcpInvocation.Status != "succeeded" ||
		mcpInvocation.CompletedAt == nil ||
		string(mcpInvocation.Arguments) != functionExactObject ||
		string(mcpInvocation.Result) != automationFunctionOutput {
		t.Fatalf("exact Automation MCP invocation: %+v", mcpInvocation)
	}
}
