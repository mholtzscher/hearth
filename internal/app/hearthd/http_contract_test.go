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
	// Includes paths, wire schemas, component names, references and publication
	// metadata. Intentional API extensions update this digest alongside contract tests.
	got := fmt.Sprintf("%x", sha256.Sum256(response.Body.Bytes()))
	const want = "77f722d56c4684e86a89b341fd08f0ce3c95eeaff745c6058f62455e5dcf2523"
	if got != want {
		t.Fatalf("HTTP/OpenAPI contract changed: SHA-256 = %s, want %s", got, want)
	}
}
