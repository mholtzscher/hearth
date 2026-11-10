//go:build integration

package agent //nolint:testpackage // Shared real dashboard MCP fixtures.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	dashboardmcp "github.com/mholtzscher/hearth/internal/modules/dashboards/mcpapi"
	platformmcp "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func dashboardDefinitionFixture(t *testing.T, title string, large bool) json.RawMessage {
	t.Helper()
	elements := map[string]any{}
	children := []string{"a"}
	text := "Hello"
	if large {
		children = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
		text = strings.Repeat("x", 490)
	}
	for _, child := range children {
		elements[child] = map[string]any{"type": "Note", "props": map[string]any{"text": text}}
	}
	elements["root"] = map[string]any{"type": "Stack", "props": map[string]any{"gap": "small"}, "children": children}
	raw, err := json.Marshal(
		map[string]any{
			"schema_version":  1,
			"catalog_version": "hearth.dashboard/v1",
			"title":           title,
			"spec":            map[string]any{"root": "root", "elements": elements},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dashboards.ParseDefinition(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func dashboardMCPServer(presenter dashboardmcp.Presenter) *platformmcp.Server {
	server := platformmcp.New(platformmcp.Config{Name: "dashboard-agent-test", Version: "1"})
	dashboardmcp.Register(server, presenter)
	return server
}

func dashboardAdvertisedSchema(t *testing.T, server *platformmcp.Server) map[string]any {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Raw().Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "schema-test", Version: "1"}, nil).
		Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	listed, err := client.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != 1 {
		t.Fatalf("list tools: %+v, %v", listed, err)
	}
	raw, err := json.Marshal(listed.Tools[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func assertAgentPresentationSchema(t *testing.T, got, advertised map[string]any) {
	t.Helper()
	properties, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing model properties")
	}
	flag, ok := properties["complete_turn"].(map[string]any)
	if !ok || flag["type"] != "boolean" {
		t.Fatal("missing boolean completion flag")
	}
	delete(properties, "complete_turn")
	if !reflect.DeepEqual(got, advertised) {
		t.Fatal("MCP schema changed beyond agent-only completion flag")
	}
}

type coordinatedPresenter struct {
	present func(context.Context, dashboards.Definition) (dashboards.Artifact, error)
}

func (p coordinatedPresenter) PresentView(
	ctx context.Context,
	definition dashboards.Definition,
) (dashboards.Artifact, error) {
	return p.present(ctx, definition)
}

func publicHistoryJSON(t *testing.T, service *Service, conversationID string) json.RawMessage {
	t.Helper()
	handler := &Handler{service: service}
	output, err := handler.History(t.Context(), &HistoryInput{ID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(output.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
