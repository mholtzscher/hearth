//go:build integration

package mcpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type checkedInput struct {
	Label    string            `json:"label"`
	Document mcpapi.JSONObject `json:"document"`
	Count    int               `json:"count,omitempty" default:"3"`
}

func (input *checkedInput) UnmarshalJSON(raw []byte) error {
	type arguments checkedInput
	var decoded arguments
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	switch decoded.Label {
	case "modeled":
		return &mcpapi.ToolError{Code: "invalid_document", Message: "invalid document"}
	case "internal":
		return mcpapi.InternalToolError(errors.New("PRIVATE-input-cause"))
	case "unexpected":
		return errors.New("PRIVATE-decoder-cause")
	case "protocol":
		return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid arguments"}
	}
	*input = checkedInput(decoded)
	return nil
}

func inputSession(t *testing.T, server *mcpapi.Server, transport string) *mcp.ClientSession {
	t.Helper()
	if transport == "http" {
		return connectSession(t, server.HTTPHandler())
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Raw().Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "input-contracts", Version: "1"}, nil).
		Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestSharedInputPipelineOverBothTransports(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"http", "memory"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := mcpapi.New(mcpapi.Config{Name: "input-contracts", Version: "1", Logger: discardLogger()})
			mcpapi.Register(server, mcpapi.Tool[checkedInput, greetOutput]{Name: "checked",
				Handler: func(_ context.Context, input checkedInput) (greetOutput, error) {
					calls.Add(1)
					if input.Label != "office" || input.Count != 3 ||
						!strings.Contains(string(input.Document), `9007199254740993`) ||
						!strings.Contains(string(input.Document), `1.0000000000000001`) {
						t.Errorf("wrong prepared input: %+v document=%s", input, input.Document)
					}
					return greetOutput{Greeting: "saved"}, nil
				},
			})
			mcpapi.Register(server, mcpapi.Tool[noInput, greetOutput]{
				Name:    "ordinary",
				Handler: func(context.Context, noInput) (greetOutput, error) { return greetOutput{Greeting: transport}, nil },
			})
			session := inputSession(t, server, transport)
			assertDecoderFailures(t, session)
			assertInvalidInputEnvelopes(t, session)
			if calls.Load() != 0 {
				t.Fatalf("invalid inputs admitted: %d", calls.Load())
			}
			result, err := session.CallTool(
				t.Context(),
				&mcp.CallToolParams{
					Name: "checked",
					Arguments: json.RawMessage(
						`{"label":"office","document":{"integer":9007199254740993,"decimal":1.0000000000000001,"nested":[null]}}`,
					),
				},
			)
			if err != nil || result.IsError || firstText(t, result) != `{"greeting":"saved"}` || calls.Load() != 1 {
				t.Fatalf("valid input did not reach the handler once: %+v %v calls=%d", result, err, calls.Load())
			}
			ordinary, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "ordinary"})
			if err != nil || ordinary.IsError || !strings.Contains(firstText(t, ordinary), transport) {
				t.Fatalf("ordinary tool contaminated: %+v %v", ordinary, err)
			}
		})
	}
}

func assertDecoderFailures(t *testing.T, session *mcp.ClientSession) {
	t.Helper()
	for _, test := range []struct{ label, code, text string }{
		{"modeled", "invalid_document", ""}, {"internal", "internal_error", ""}, {"unexpected", "", "internal error"},
	} {
		result, err := session.CallTool(
			t.Context(),
			&mcp.CallToolParams{
				Name:      "checked",
				Arguments: map[string]any{"label": test.label, "document": map[string]any{}},
			},
		)
		if err != nil || !result.IsError {
			t.Fatalf("%s: result=%+v err=%v", test.label, result, err)
		}
		text := firstText(t, result)
		if strings.Contains(text, "PRIVATE") {
			t.Fatalf("private decoder cause leaked: %s", text)
		}
		if test.code == "" {
			if text != test.text || result.StructuredContent != nil {
				t.Fatalf("unexpected failure changed: %+v", result)
			}
			continue
		}
		assertModeledInputFailure(t, result, test.code)
	}
	_, err := session.CallTool(
		t.Context(),
		&mcp.CallToolParams{
			Name:      "checked",
			Arguments: map[string]any{"label": "protocol", "document": map[string]any{}},
		},
	)
	protocol, ok := errors.AsType[*jsonrpc.Error](err)
	if !ok || protocol.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("protocol error changed: %v", err)
	}
}

func assertModeledInputFailure(t *testing.T, result *mcp.CallToolResult, code string) {
	t.Helper()
	text := firstText(t, result)
	var failure map[string]any
	if err := json.Unmarshal([]byte(text), &failure); err != nil || failure["failure_code"] != code {
		t.Fatalf("failure text=%s err=%v", text, err)
	}
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil || text != string(structured) {
		t.Fatalf("modeled text differs: %s %s %v", text, structured, err)
	}
}

func assertInvalidInputEnvelopes(t *testing.T, session *mcp.ClientSession) {
	t.Helper()
	for _, raw := range []string{`{}`, `{"label":"office","document":{},"extra":true}`, `{"label":"office","document":[],"count":3}`} {
		result, callErr := session.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: "checked", Arguments: json.RawMessage(raw)},
		)
		if callErr != nil || !result.IsError ||
			!strings.Contains(firstText(t, result), `"failure_code":"invalid_arguments"`) {
			t.Fatalf("invalid envelope=%s result=%+v err=%v", raw, result, callErr)
		}
	}
}

func TestSharedInMemoryHandlerObservesCallerCancellation(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "cancellation", Version: "1", Logger: discardLogger()})
	started := make(chan struct{})
	observed := make(chan error, 1)
	mcpapi.Register(server, mcpapi.Tool[noInput, greetOutput]{Name: "wait",
		Handler: func(ctx context.Context, _ noInput) (greetOutput, error) {
			close(started)
			<-ctx.Done()
			observed <- ctx.Err()
			return greetOutput{}, ctx.Err()
		},
	})
	session := inputSession(t, server, "memory")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wait"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler cancellation=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach handler")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("caller did not return")
	}
}
