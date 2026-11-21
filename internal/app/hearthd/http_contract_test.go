package hearthd //nolint:testpackage // Pins the assembled HTTP contract across transport moves.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/household"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestHTTPTransportContractSnapshot(t *testing.T) {
	t.Parallel()
	handler, _ := newHTTPHandler(
		nil,
		&stubDevices{},
		&stubAutomations{},
		&stubNotifications{},
		&stubDevices{},
		&stubFunctions{},
		&stubAgent{},
		&testReadiness{},
		&stubDevices{},
		&stubAutomations{},
		&stubFunctions{},
		mcpapi.New(
			mcpapi.Config{Name: mcpName, Version: mcpVersion},
		),
		dashboards.NewService(nil, &stubDevices{}, nil),
		household.Settings{},
	)
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d", response.Code)
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	components, ok := document["components"].(map[string]any)
	if !ok {
		t.Fatal("missing OpenAPI components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("missing OpenAPI schemas")
	}
	input := schemas["MessageInput"].(map[string]any)["properties"].(map[string]any)
	contextSchema := input["automation_context"].(map[string]any)
	if len(contextSchema["oneOf"].([]any)) != 2 {
		t.Fatal("automation context must publish create and replace variants")
	}
	turn := schemas["TurnBody"].(map[string]any)["properties"].(map[string]any)
	artifactSchema := turn["artifacts"].(map[string]any)["items"].(map[string]any)
	if len(artifactSchema["oneOf"].([]any)) != 2 {
		t.Fatal("turn artifacts must publish view and automation variants")
	}
	entity := schemas["EntityBody"].(map[string]any)["properties"].(map[string]any)
	if _, exists := entity["quantities"]; !exists {
		t.Fatal("Entity schema must publish quantities")
	}
	paths := document["paths"].(map[string]any)
	assertEntityReadContract(t, paths, schemas)
	// Includes paths, wire schemas, component names, references and publication
	// metadata. Intentional API extensions update this digest alongside contract tests.
	got := fmt.Sprintf("%x", sha256.Sum256(response.Body.Bytes()))
	const want = "caceeef05c75cc92360bbc4fb946e9598083a800a65e95982342d7928248df0b"
	if got != want {
		t.Fatalf("HTTP/OpenAPI contract changed: SHA-256 = %s, want %s", got, want)
	}
}

func assertEntityReadContract(t *testing.T, paths, schemas map[string]any) {
	t.Helper()
	for _, operation := range []struct{ path, method, parameter string }{
		{"/v1/entities", "get", "units"},
		{"/v1/entities/{entity_id}", "patch", "units"}, {"/v1/devices/{device_id}", "get", "units"},
	} {
		published := paths[operation.path].(map[string]any)[operation.method].(map[string]any)
		found := false
		for _, parameter := range published["parameters"].([]any) {
			p := parameter.(map[string]any)
			if p["name"] == operation.parameter && p["in"] == "query" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s %s does not publish %s", operation.method, operation.path, operation.parameter)
		}
	}
	for _, operation := range []struct{ path, method string }{
		{"/v1/entities/{entity_id}", "get"}, {"/v1/entities/{entity_id}/state/history", "post"},
	} {
		published := paths[operation.path].(map[string]any)[operation.method].(map[string]any)
		for _, parameter := range published["parameters"].([]any) {
			p := parameter.(map[string]any)
			if p["in"] == "query" && (p["name"] == "units" || p["name"] == "include") {
				t.Fatalf("%s %s still publishes selector %s", operation.method, operation.path, p["name"])
			}
		}
	}
	assertPairedReadSchemas(t, paths, schemas)
}

func assertPairedReadSchemas(t *testing.T, paths, schemas map[string]any) {
	t.Helper()
	paired := schemas["PairedStateBody"].(map[string]any)["properties"].(map[string]any)
	for _, property := range []string{"canonical", "display", "observation_id"} {
		if _, exists := paired[property]; !exists {
			t.Fatalf("paired State schema missing %s", property)
		}
	}
	if _, exists := paired["value"]; exists {
		t.Fatal("paired State schema must not publish legacy value")
	}
	detail := paths["/v1/entities/{entity_id}"].(map[string]any)["get"].(map[string]any)
	detailResponse := detail["responses"].(map[string]any)["200"].(map[string]any)
	detailSchema := detailResponse["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if detailSchema["$ref"] != "#/components/schemas/PairedEntityBody" {
		t.Fatalf("Entity detail must publish the paired schema: %#v", detailSchema)
	}
	history := paths["/v1/entities/{entity_id}/state/history"].(map[string]any)["post"].(map[string]any)
	historyResponse := history["responses"].(map[string]any)["200"].(map[string]any)
	historySchema := historyResponse["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	variants := historySchema["oneOf"].([]any)
	if len(variants) != 2 {
		t.Fatalf("history must publish summary and bucketed variants only: %#v", variants)
	}
	summary := variants[0].(map[string]any)["properties"].(map[string]any)
	bucketed := variants[1].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"canonical", "display"} {
		if _, ok := summary[field]; !ok {
			t.Fatalf("history summary missing %s", field)
		}
	}
	if _, ok := bucketed["buckets"]; !ok {
		t.Fatal("history bucketed variant missing buckets")
	}
	if _, ok := summary["quantity"]; ok {
		t.Fatal("history summary publishes legacy quantity")
	}
}
