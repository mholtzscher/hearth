//go:build integration

package sqlite //nolint:testpackage // Real transaction errors flow through registered protocol owners.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationshttp "github.com/mholtzscher/hearth/internal/modules/automations/httpapi"
	automationsmcp "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func assertRollbackProtocolFailures(t *testing.T, cause error) {
	t.Helper()
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if canceled {
			cancel()
		}
		assertRollbackHTTP(ctx, t, cause)
		// The MCP SDK does not promise a response after request cancellation.
		// Assert the wire envelope while connected; HTTP separately pins the
		// ended-owner diagnostic path, and cause matching is checked at SQLite.
		if !canceled {
			assertRollbackMCP(ctx, t, cause)
		}
		cancel()
	}
}

func assertRollbackHTTP(ctx context.Context, t *testing.T, cause error) {
	t.Helper()
	var logs bytes.Buffer
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Transaction", "test"))
	automationshttp.Register(api, &transactionFailureService{cause: cause}, slog.New(slog.NewJSONHandler(&logs, nil)))
	request := httptest.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		"/automations/aut_01890f47-7a6b-7c4d-8e9f-0123456789ab?expected_revision=1",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != 500 || body["code"] != "internal_error" || body["detail"] != "internal error" {
		t.Fatalf("changed rollback classification: %d %s", response.Code, response.Body.String())
	}
	assertRollbackDiagnostic(t, logs.Bytes(), "automation.http_internal_failure")
	assertRollbackPrivacy(t, response.Body.String()+fmt.Sprint(response.Header())+logs.String())
}

func assertRollbackMCP(ctx context.Context, t *testing.T, cause error) {
	t.Helper()
	var logs bytes.Buffer
	server := mcptools.New(
		mcptools.Config{Name: "Transaction", Version: "test", Logger: slog.New(slog.NewJSONHandler(&logs, nil))},
	)
	automationsmcp.Register(server, &transactionFailureService{cause: cause})
	request := httptest.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"/mcp",
		strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_automation","arguments":{"automation_id":"aut_01890f47-7a6b-7c4d-8e9f-0123456789ab","expected_revision":1}}}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	payload := response.Body.String()
	for line := range strings.SplitSeq(payload, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			payload = data
			break
		}
	}
	var wire struct {
		Result struct {
			IsError    bool           `json:"isError"`
			Structured map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(payload), &wire); err != nil {
		t.Fatalf("MCP response=%s: %v", response.Body.String(), err)
	}
	if !wire.Result.IsError || wire.Result.Structured["failure_code"] != "internal_error" ||
		wire.Result.Structured["message"] != "internal error" ||
		len(wire.Result.Structured) != 2 {
		t.Fatalf("changed MCP failure: %s", payload)
	}
	assertRollbackDiagnostic(t, logs.Bytes(), "mcp.tool_internal_failure")
	assertRollbackPrivacy(t, response.Body.String()+logs.String())
}

func assertRollbackDiagnostic(t *testing.T, logs []byte, event string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(logs, &record); err != nil {
		t.Fatalf("want one diagnostic: %v; logs=%s", err, logs)
	}
	if record["event"] != event || record["error_code"] != "internal_error" {
		t.Fatalf("wrong diagnostic: %v", record)
	}
}

func assertRollbackPrivacy(t *testing.T, text string) {
	t.Helper()
	for _, private := range []string{"SENSITIVE", "no transaction is active", "already been committed or rolled back", "automation revision conflict"} {
		if strings.Contains(text, private) {
			t.Fatalf("private error text leaked: %q", private)
		}
	}
}

type transactionFailureService struct {
	automationshttp.Automations

	cause error
}

func (service *transactionFailureService) DeleteAutomation(context.Context, automations.AutomationID, int64) error {
	return service.cause
}
