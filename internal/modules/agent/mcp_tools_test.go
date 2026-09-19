package agent //nolint:testpackage // Tests exercise the in-memory MCP bridge internals.

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

type bridgeEchoInput struct {
	Text string `json:"text"`
}

type bridgeEchoOutput struct {
	Echo string `json:"echo"`
}

type bridgeCountInput struct {
	Name  string `json:"name"`
	Count *int   `json:"count,omitempty"`
}

type bridgeCountOutput struct {
	Summary string `json:"summary"`
}

func newBridgeTestServer() *mcpapi.Server {
	server := mcpapi.New(mcpapi.Config{Name: "hearth-agent-test", Version: "0.0.0"})
	mcpapi.Register(server, mcpapi.Tool[bridgeEchoInput, bridgeEchoOutput]{
		Name: "echo_text", Description: "Echo the input text back",
		Handler: func(_ context.Context, input bridgeEchoInput) (bridgeEchoOutput, error) {
			return bridgeEchoOutput{Echo: input.Text}, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[bridgeEchoInput, bridgeEchoOutput]{
		Name: "fail_example", Description: "Always fail with a domain error",
		Handler: func(_ context.Context, _ bridgeEchoInput) (bridgeEchoOutput, error) {
			return bridgeEchoOutput{}, &mcpapi.ToolError{Code: "example_disabled", Message: "turned off"}
		},
	})
	mcpapi.Register(server, mcpapi.Tool[bridgeCountInput, bridgeCountOutput]{
		Name: "needs_count", Description: "Require a name with an optional count",
		Handler: func(_ context.Context, input bridgeCountInput) (bridgeCountOutput, error) {
			return bridgeCountOutput{Summary: input.Name}, nil
		},
	})
	return server
}

func mustBridgeTools(t *testing.T, server *mcpapi.Server) []tool.BaseTool {
	t.Helper()
	bridged, err := MCPTools(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	return bridged
}

func bridgeToolByName(t *testing.T, bridged []tool.BaseTool, name string) tool.InvokableTool {
	t.Helper()
	for _, candidate := range bridged {
		info, err := candidate.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != name {
			continue
		}
		if info.ParamsOneOf == nil {
			t.Fatalf("%s has no parameter schema", name)
		}
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			t.Fatalf("%s is not invokable", name)
		}
		return invokable
	}
	t.Fatalf("no bridged tool named %s", name)
	return nil
}

func TestMCPToolsBridgeCatalog(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())
	if len(bridged) != 3 {
		t.Fatalf("bridged %d tools, want 3", len(bridged))
	}
	names := map[string]bool{}
	for _, candidate := range bridged {
		info, err := candidate.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		names[info.Name] = true
		if info.Desc == "" {
			t.Fatalf("%s has no description", info.Name)
		}
	}
	for _, want := range []string{"echo_text", "fail_example", "needs_count"} {
		if !names[want] {
			t.Fatalf("bridged catalog missing %s: %v", want, names)
		}
	}
}

func TestMCPToolsBridgeInvoke(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())

	echo := bridgeToolByName(t, bridged, "echo_text")
	out, err := echo.InvokableRun(t.Context(), `{"text":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("echo result = %q, want the input text", out)
	}
}

func TestMCPToolsBridgeDomainError(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())

	failing := bridgeToolByName(t, bridged, "fail_example")
	_, err := failing.InvokableRun(t.Context(), `{"text":"hi"}`)
	if err == nil {
		t.Fatal("isError result produced no error")
	}
	if !strings.Contains(err.Error(), "example_disabled") {
		t.Fatalf("error = %q, want the domain failure code", err.Error())
	}
}

func TestMCPToolsBridgeRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())

	needsName := bridgeToolByName(t, bridged, "needs_count")
	if _, err := needsName.InvokableRun(t.Context(), `{"count":2}`); err == nil {
		t.Fatal("missing required argument produced no error")
	}
}

func TestMCPToolsRequiresServer(t *testing.T) {
	t.Parallel()
	if _, err := MCPTools(t.Context(), nil); err == nil {
		t.Fatal("nil server produced no error")
	}
}
