//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestPresentAutomationIsReadOnlyAndRetainsExactDefinition(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	server := mcptools.New(mcptools.Config{Name: "presentation", Version: "1"})
	automationsapi.Register(server, service)
	definition := precisionDefinitionDocument(t, "9007199254740993")
	created := callAutomationWireTool(t, server, "create_automation", `{"definition":`+definition+`}`)
	if created.IsError {
		t.Fatalf("create: %s", created.Structured)
	}
	var receipt AutomationReceiptRead
	if err := json.Unmarshal(created.Structured, &receipt); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{
		`{"mode":"reference","automation_id":"` + receipt.ID + `"}`,
		`{"mode":"create","definition":` + definition + `}`,
		`{"mode":"replace","automation_id":"` + receipt.ID + `","expected_revision":1,"definition":` + definition + `}`,
	} {
		result := callAutomationWireTool(t, server, "present_automation", args)
		if result.IsError {
			t.Fatalf("present %s: %s", args, result.Structured)
		}
		artifact, err := automations.ParseArtifact(result.Structured)
		if err != nil {
			t.Fatalf("invalid artifact %s: %v", result.Structured, err)
		}
		if !strings.Contains(args, `"mode":"reference"`) &&
			!strings.Contains(string(result.Structured), "9007199254740993") {
			t.Fatalf("lost exact value: %+v", artifact)
		}
	}
	assertPresentationExactSources(t, server)
	for _, args := range []string{
		`{"mode":"reference","automation_id":"` + receipt.ID + `","definition":` + definition + `}`,
		`{"mode":"create","definition":` + definition + `,"unknown":true}`,
		`{"mode":"replace","automation_id":"` + receipt.ID + `","expected_revision":0,"definition":` + definition + `}`,
		`{"mode":"reference","automation_id":"aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"}`,
		`{"mode":"create","definition":{"name":"Missing child","enabled":false,"triggers":[],"steps":[{"id":"call","kind":"sub_automation","automation_id":"aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"}]}}`,
	} {
		if result := callAutomationWireTool(t, server, "present_automation", args); !result.IsError {
			t.Fatalf("accepted %s", args)
		}
	}
	read := callAutomationWireTool(t, server, "get_automation", `{"automation_id":"`+receipt.ID+`"}`)
	if read.IsError || !strings.Contains(string(read.Structured), `"revision":1`) {
		t.Fatalf("presentation modified saved definition: %s", read.Structured)
	}
	list := callAutomationWireTool(t, server, "list_automations", `{}`)
	if list.IsError || strings.Count(string(list.Structured), receipt.ID) != 1 {
		t.Fatalf("presentation created a definition: %s", list.Structured)
	}
	history, err := service.ListHistory(
		t.Context(),
		automations.ListHistoryParams{AutomationID: automations.AutomationID(receipt.ID), Limit: 10},
	)
	if err != nil || len(history.Items) != 0 {
		t.Fatalf("presentation admitted a Run: %+v %v", history, err)
	}
}

func assertPresentationExactSources(t *testing.T, server *mcptools.Server) {
	t.Helper()
	for _, literal := range []string{"9007199254740993", "1.0000000000000001", "1e10000", "1e-10000"} {
		source := precisionDefinitionDocument(t, literal)
		result := callAutomationWireTool(t, server, "present_automation", `{"mode":"create","definition":`+source+`}`)
		if result.IsError {
			t.Fatalf("present %s: %s", literal, result.Structured)
		}
		var output struct {
			DefinitionJSON string `json:"definition_json"`
		}
		if err := json.Unmarshal(result.Structured, &output); err != nil || output.DefinitionJSON != source {
			t.Fatalf("source changed for %s: %s %v", literal, output.DefinitionJSON, err)
		}
	}
}
