//go:build e2e

package hearthd //nolint:testpackage // Exercises production Core via HTTP, MCP, and the public Adapter SDK.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// A9/A10: both authoring transports retain exact JSON, discover the same stable
// contract, and link real SDK work to both durable histories. A new Session may
// change routing revision without repinning the Automation; a changed declared
// timeout must instead fail input validation before reservation or dispatch and
// retain a value failure with no invented external link.
//
//nolint:gocognit,gocyclo,cyclop // One public journey keeps the restart and linked evidence assertions together.
func TestAutomationFunctionsPublicJourneyAndAdapterRestart(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"http", "mcp"} {
		t.Run(protocol, func(t *testing.T) {
			t.Parallel()
			broker := startLifecycleNATSServer(t)
			core := startFunctionCore(t, Config{HouseholdTimezone: "UTC", NATSURL: broker.ClientURL(),
				SQLitePath: filepath.Join(t.TempDir(), "core.db"), Agent: requiredAgentConfig(t)})
			client := newNonPoolingHTTPClient(t)
			mcpClient := connectAutomationFunctionsMCP(t, core)
			source := startSliceAdapter(t.Context(), t, broker, core.address)
			t.Cleanup(source.stop)
			calls := make(chan adapter.FunctionRequest, 8)
			session := startAutomationFunctionSDK(t, broker.ClientURL(), 60000, calls)
			first := discoverAutomationFunction(t, client, core, mcpClient)
			definition := automationFunctionDefinition(source.eventEntityID, first.ContractFingerprint)
			var automationID string
			if protocol == "http" {
				automationID = createBranchingAutomation(t.Context(), t, core.address, definition)
			} else {
				receipt := decodeNotification[struct {
					AutomationID string `json:"automation_id"`
					Revision     int64  `json:"revision"`
				}](t, automationFunctionsMCP(t, mcpClient, "create_automation", `{"definition":`+definition+`}`))
				if receipt.AutomationID == "" || receipt.Revision != 1 {
					t.Fatalf("MCP create receipt: %+v", receipt)
				}
				automationID = receipt.AutomationID
			}
			path := "/v1/automations/" + automationID
			read := successfulFunctionCoreRequest(t, client, core, http.MethodGet, path, "")
			assertAutomationFunctionDefinition(t, read, first.ContractFingerprint, 1)
			mcpRead := automationFunctionsMCP(
				t,
				mcpClient,
				"get_automation",
				fmt.Sprintf(`{"automation_id":%q}`, automationID),
			)
			assertAutomationFunctionDefinition(t, mcpRead, first.ContractFingerprint, 1)
			// An unrelated edit must preserve, rather than silently repin, the contract.
			replacement := strings.Replace(definition, "Inspect precise values", "Renamed precise inspection", 1)
			if protocol == "http" {
				raw := successfulFunctionCoreRequest(t, client, core, http.MethodPut, path,
					`{"expected_revision":1,"definition":`+replacement+`}`)
				assertAutomationFunctionDefinition(t, raw, first.ContractFingerprint, 2)
			} else {
				automationFunctionsMCP(
					t,
					mcpClient,
					"replace_automation",
					fmt.Sprintf(
						`{"automation_id":%q,"expected_revision":1,"definition":%s}`,
						automationID,
						replacement,
					),
				)
			}
			previous := make(map[string]bool)
			runManual := func() string {
				t.Helper()
				response := conditionsManualRun(t.Context(), t, core.address, automationID, "")
				defer response.Body.Close()
				if response.StatusCode != http.StatusAccepted {
					t.Fatalf("manual admission: %d %s", response.StatusCode, readSliceBody(t, response))
				}
				return awaitAutomationFunctionRun(t, client, core, automationID, previous)
			}
			firstID := assertAutomationFunctionSuccess(t, client, core, mcpClient, automationID, runManual(),
				first.CatalogRevision, "manual", calls)
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			session = startAutomationFunctionSDK(t, broker.ClientURL(), 60000, calls)
			second := discoverAutomationFunction(t, client, core, mcpClient)
			if second.CatalogRevision == first.CatalogRevision ||
				second.ContractFingerprint != first.ContractFingerprint {
				t.Fatalf("same-contract restart did not change routing only: %+v / %+v", first, second)
			}
			secondID := assertAutomationFunctionSuccess(t, client, core, mcpClient, automationID, runManual(),
				second.CatalogRevision, "manual", calls)
			if firstID == secondID {
				t.Fatal("separate Runs reused an invocation identity")
			}
			if err := source.emit(t.Context(), sliceEntityEventSinglePress); err != nil {
				t.Fatal(err)
			}
			automaticID := awaitAutomationFunctionRun(t, client, core, automationID, previous)
			thirdID := assertAutomationFunctionSuccess(t, client, core, mcpClient, automationID, automaticID,
				second.CatalogRevision, "device_fact", calls)
			automatic := readAutomationFunctionRun(t, client, core, automationID, automaticID)
			if automatic.Cause.Fact.Family != "entity_event" || automatic.Cause.Fact.EntityID != source.eventEntityID ||
				automatic.Cause.Fact.EventID == "" || automatic.Cause.Fact.Name != sliceEntityEventSinglePress {
				t.Fatalf("automatic provenance: %+v", automatic.Cause)
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			startAutomationFunctionSDK(t, broker.ClientURL(), 59000, calls)
			changed := discoverAutomationFunction(t, client, core, mcpClient)
			if changed.ContractFingerprint == first.ContractFingerprint {
				t.Fatal("changed timeout retained the old declared-contract fingerprint")
			}
			failed := readAutomationFunctionRun(t, client, core, automationID, runManual())
			if failed.Status != "failed" || failed.FailureCode != "resolved_input_invalid" ||
				len(failed.Executions) != 1 {
				t.Fatalf("changed contract Run: %+v", failed)
			}
			execution := failed.Executions[0]
			evidence := execution.Evidence
			if execution.Kind != "function" || execution.Phase != "failed" ||
				evidence.ValueFailure == nil || evidence.ValueFailure.Path != "/steps/0/arguments" ||
				evidence.ValueFailure.FailureCode != "resolved_input_invalid" || evidence.ReasonCode != "" ||
				evidence.InvocationID != "" ||
				evidence.InvocationOutcome != "" ||
				evidence.CatalogRevision != "" ||
				execution.CompletedAt == nil {
				t.Fatalf("pre-reservation value validation invented invocation evidence: %+v", evidence)
			}
			select {
			case request := <-calls:
				t.Fatalf("changed contract dispatched or replayed: %+v", request)
			default:
			}
			for _, raw := range [][]byte{
				successfulFunctionCoreRequest(t, client, core, http.MethodGet,
					"/v1/adapters/"+automationFunctionAdapter+"/function-invocations", ""),
				automationFunctionsMCP(t, mcpClient, "list_adapter_function_invocations",
					fmt.Sprintf(`{"adapter_id":%q}`, automationFunctionAdapter)),
			} {
				page := decodeNotification[struct {
					Items []struct {
						ID           string `json:"id"`
						InvocationID string `json:"invocation_id"`
						Status       string `json:"status"`
					} `json:"items"`
				}](t, raw)
				want := map[string]bool{firstID: true, secondID: true, thirdID: true}
				if len(page.Items) != len(want) {
					t.Fatalf("one Invocation per reached call: %s", raw)
				}
				for _, item := range page.Items {
					id := item.ID
					if id == "" {
						id = item.InvocationID
					}
					if !want[id] || item.Status != "succeeded" {
						t.Fatalf("unexpected Invocation in household history: %s", raw)
					}
					delete(want, id)
				}
			}
			assertAutomationFunctionDefinition(t,
				successfulFunctionCoreRequest(t, client, core, http.MethodGet, path, ""), first.ContractFingerprint, 2)
		})
	}
}

func assertAutomationFunctionDefinition(t *testing.T, raw []byte, fingerprint string, revision int64) {
	t.Helper()
	record := decodeNotification[struct {
		Revision   int64 `json:"revision"`
		Definition struct {
			Steps []struct {
				Kind                string          `json:"kind"`
				AdapterID           string          `json:"adapter_id"`
				FunctionKey         string          `json:"function_key"`
				ContractFingerprint string          `json:"contract_fingerprint"`
				Arguments           json.RawMessage `json:"arguments"`
			} `json:"steps"`
		} `json:"definition"`
	}](t, raw)
	if record.Revision != revision || len(record.Definition.Steps) != 1 {
		t.Fatalf("definition receipt/read: %s", raw)
	}
	step := record.Definition.Steps[0]
	if step.Kind != "function" || step.AdapterID != automationFunctionAdapter || step.FunctionKey != "inspect" ||
		step.ContractFingerprint != fingerprint || string(step.Arguments) != functionExactObject {
		t.Fatalf("Function definition changed: %s", raw)
	}
}
