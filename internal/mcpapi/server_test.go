package mcpapi_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// discardLogger returns a logger that swallows records, so a test does not print
// the diagnostic it asserts elsewhere.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// connectSession starts an HTTP server over handler and returns an official MCP
// client session connected to it, mirroring how a real client reaches Core.
func connectSession(t *testing.T, handler http.Handler) *mcp.ClientSession {
	t.Helper()
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	session, err := mcp.NewClient(
		&mcp.Implementation{Name: "hearth-mcpapi-test", Version: "1.0.0"},
		nil,
	).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Errorf("close MCP session: %v", closeErr)
		}
	})
	return session
}

// decodeStructured round-trips a result's structured content into target, the
// way a typed client recovers its output value.
func decodeStructured(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if unmarshalErr := json.Unmarshal(raw, target); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
}

// firstText returns the text of the first text content block in result.
func firstText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatalf("result has no text content: %#v", result.Content)
	return ""
}

// TestNewServerServesToolsListThroughOfficialClient proves the endpoint
// initializes and lists registered tools.
func TestNewServerServesToolsListThroughOfficialClient(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, nil
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(result.Tools))
	}
	if result.Tools[0].Name != "greet" || result.Tools[0].Description != "Greet one person" {
		t.Fatalf("tool = %#v", result.Tools[0])
	}
	if result.Tools[0].InputSchema == nil || result.Tools[0].OutputSchema == nil {
		t.Fatalf("tool schemas missing: %#v", result.Tools[0])
	}
}

// TestHTTPHandlerIsStateless proves the Streamable HTTP handler does not keep
// session affinity: it rejects non-POST requests, as stateless mode documents.
func TestHTTPHandlerIsStateless(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	httpServer := httptest.NewServer(server.HTTPHandler())
	t.Cleanup(httpServer.Close)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpServer.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET MCP endpoint: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	if allow := response.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", allow, http.MethodPost)
	}
}

// TestRawExposesServedSDKServer proves Raw escapes to the same SDK server the
// wrapper serves, so unsupported features share one registration target.
func TestRawExposesServedSDKServer(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	raw := server.Raw()
	if raw == nil {
		t.Fatal("Raw() = nil")
	}
	if raw != server.Raw() {
		t.Fatal("Raw() returned different servers")
	}
	raw.AddTool(
		&mcp.Tool{Name: "raw_tool", Description: "added through Raw", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "raw"}}}, nil
		},
	)

	session := connectSession(t, server.HTTPHandler())
	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "raw_tool" {
		t.Fatalf("tools = %#v, want the tool added through Raw", result.Tools)
	}
}

// TestHTTPHandlerPropagatesRequestCancellation proves the Streamable HTTP
// handler ties an in-flight tool handler to the client request, so a dropped
// client unwinds the handler exactly as it unwinds the matching REST handler.
// The SDK only applies the option to protocol 2026-07-28 and later, which the
// official client negotiates by default.
func TestHTTPHandlerPropagatesRequestCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0", Logger: discardLogger()})
	mcpapi.Register(server, mcpapi.Tool[noInput, greetOutput]{
		Name:        "block_until_disconnect",
		Description: "Block until the caller disconnects",
		Handler: func(ctx context.Context, _ noInput) (greetOutput, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return greetOutput{}, ctx.Err()
		},
	})

	session := connectSession(t, server.HTTPHandler())
	if version := session.InitializeResult().ProtocolVersion; version < "2026-07-28" {
		t.Skipf("request cancellation applies only to protocol >= 2026-07-28, got %q", version)
	}

	callContext, cancelCall := context.WithCancel(t.Context())
	t.Cleanup(cancelCall)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The client call is expected to fail once its context is cancelled; the
		// assertion is that the server handler unwound, not how the client
		// surfaces the dropped connection.
		_, _ = session.CallTool(callContext, &mcp.CallToolParams{
			Name: "block_until_disconnect", Arguments: map[string]any{},
		})
	}()

	<-started
	cancelCall()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler context was not cancelled when the client disconnected")
	}
	<-done
}
