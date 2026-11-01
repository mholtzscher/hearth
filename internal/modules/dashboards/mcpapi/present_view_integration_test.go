//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestPresentViewCatalogAndCall(t *testing.T) {
	t.Parallel()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	mcpapi.Register(server, dashboards.NewService(nil, nil))
	httpServer := httptest.NewServer(server.HTTPHandler())
	defer httpServer.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != 1 {
		t.Fatalf("list: %v %+v", err, listed)
	}
	raw, err := json.Marshal(listed.Tools[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if decodeErr := json.Unmarshal(raw, &schema); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	props := schema["properties"].(map[string]any)
	actual := props["definition"].(map[string]any)
	var expected map[string]any
	if decodeErr := json.Unmarshal(viewsv1.Schema, &expected); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	expectedRaw, _ := json.Marshal(portable(expected))
	actualRaw, _ := json.Marshal(actual)
	if string(actualRaw) != string(expectedRaw) {
		t.Fatalf("schema differs: %s", actualRaw)
	}
	definition := map[string]any{
		"schema_version":  1,
		"catalog_version": "hearth.dashboard/v1",
		"title":           "View",
		"spec": map[string]any{
			"root":     "a",
			"elements": map[string]any{"a": map[string]any{"type": "Note", "props": map[string]any{"text": "Hello"}}},
		},
	}
	result, err := session.CallTool(
		t.Context(),
		&mcp.CallToolParams{Name: "present_view", Arguments: map[string]any{"definition": definition}},
	)
	if err != nil || result.IsError {
		t.Fatalf("call: %+v %v", result, err)
	}
}

// MCP's portable schema representation rewrites boolean false to an equivalent
// impossible schema; all other catalog constraints must remain intact.
func portable(value any) any {
	switch v := value.(type) {
	case bool:
		if !v {
			return map[string]any{"type": "null", "not": map[string]any{"type": "null"}}
		}
		return v
	case map[string]any:
		for key, child := range v {
			v[key] = portable(child)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = portable(child)
		}
		return v
	default:
		return value
	}
}
