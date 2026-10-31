//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// TestAutomationMCPDefinitionNumberSurvivesPersistenceAndToolRead checks exact
// input, repository storage, and client-visible definition output together.
func TestAutomationMCPDefinitionNumberSurvivesPersistenceAndToolRead(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	const literal = "9007199254740993"

	created := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "create_automation",
		json.RawMessage(`{"definition":`+precisionDefinitionDocument(t, literal)+`}`),
	))
	result := callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": created.ID})
	if result.IsError {
		t.Fatalf("get_automation failed: %s", toolErrorText(t, result))
	}
	body := exactToolBody(t, result)
	definition := body["definition"].(map[string]any)
	step := definition["steps"].([]any)[0].(map[string]any)
	if number := step["parameters"].(map[string]any)["level"]; number != json.Number(
		literal,
	) {
		t.Fatalf("persisted tool output number = %v, want exact %s", number, literal)
	}
}

func TestAutomationMCPExactRawSaveReplaceAndPersistedRead(t *testing.T) {
	t.Parallel()
	for _, literal := range []string{"9007199254740993", "1.0000000000000001", "1e10000", "1e-10000"} {
		t.Run(literal, func(t *testing.T) {
			t.Parallel()
			service := newAutomationService(t, newAPIDevices())
			server := mcptools.New(mcptools.Config{Name: "precision", Version: "1"})
			automationsapi.Register(server, service)
			definition := precisionDefinitionDocument(t, literal)
			created := callAutomationWireTool(t, server, "create_automation", `{"definition":`+definition+`}`)
			if created.IsError {
				t.Fatalf("create: %s", created.Structured)
			}
			var receipt AutomationReceiptRead
			if err := json.Unmarshal(created.Structured, &receipt); err != nil {
				t.Fatal(err)
			}
			replaced := callAutomationWireTool(t, server, "replace_automation",
				`{"automation_id":"`+receipt.ID+`","expected_revision":1e0,"definition":`+definition+`}`)
			if replaced.IsError {
				t.Fatalf("replace: %s", replaced.Structured)
			}
			read := callAutomationWireTool(t, server, "get_automation", `{"automation_id":"`+receipt.ID+`"}`)
			if read.IsError || !strings.Contains(string(read.Structured), `"level":`+literal) ||
				!strings.Contains(string(read.Structured), `"revision":2`) {
				t.Fatalf("persisted read lost exact input: %s", read.Structured)
			}
		})
	}
}

func TestAutomationMCPCanonicalRawSizeSurvivesBinding(t *testing.T) {
	t.Parallel()
	const canonicalLimit = 128 * 1024
	for _, tool := range []string{"create_automation", "replace_automation"} {
		for _, extra := range []int{0, 1} {
			t.Run(tool+strconv.Itoa(extra), func(t *testing.T) {
				t.Parallel()
				recorder := newRecordingAutomations()
				server := mcptools.New(mcptools.Config{Name: "canonical-size", Version: "1"})
				automationsapi.Register(server, recorder)
				definition := definitionDocument(t, 1)
				definition = definition[:len(definition)-1] + strings.Repeat(
					" ",
					canonicalLimit+extra-len(definition),
				) + `}`
				arguments := `{"definition":` + definition
				if tool == "replace_automation" {
					arguments += `,"automation_id":"` + createdIDForInput(t) + `","expected_revision":1`
				}
				result := callAutomationWireTool(t, server, tool, arguments+`}`)
				if result.IsError != (extra > 0) || recorder.callCount != 1-extra {
					t.Fatalf("size=%d result=%s calls=%d", len(definition), result.Structured, recorder.callCount)
				}
				if result.IsError &&
					!strings.Contains(string(result.Structured), `"failure_code":"invalid_automation"`) {
					t.Fatalf(
						"oversized canonical document admitted: %s calls=%d",
						result.Structured,
						recorder.callCount,
					)
				}
			})
		}
	}
}
