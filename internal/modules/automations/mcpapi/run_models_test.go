package mcpapi //nolint:testpackage // Verify the shared receipt mapping of every domain lifecycle variant.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestRunReceiptReportsReturnedLifecycle(t *testing.T) {
	t.Parallel()
	completed := time.Date(2026, 10, 21, 12, 0, 0, 123, time.UTC)
	for _, test := range []struct {
		name  string
		state automations.RunState
		want  string
	}{
		{"running", automations.RunningRun{}, `{"automation_id":"aut","run_id":"run","status":"running"}`},
		{"succeeded", automations.CompletedRun{CompletedAt: completed, Outcome: automations.SucceededRun{}}, `{"automation_id":"aut","run_id":"run","status":"succeeded","completed_at":"2026-10-21T12:00:00.000000123Z"}`},
		{"failed", automations.CompletedRun{CompletedAt: completed, Outcome: automations.FailedRun{FailureCode: "executor_fault"}}, `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":"2026-10-21T12:00:00.000000123Z","failure_code":"executor_fault"}`},
		{"interrupted", automations.CompletedRun{CompletedAt: completed, Outcome: automations.InterruptedRun{FailureCode: "core_restarted"}}, `{"automation_id":"aut","run_id":"run","status":"interrupted","completed_at":"2026-10-21T12:00:00.000000123Z","failure_code":"core_restarted"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			receipt := runReceipt(automations.Run{ID: "run", AutomationID: "aut", State: test.state})
			raw, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != test.want {
				t.Fatalf("receipt = %s, want %s", raw, test.want)
			}
		})
	}
}

func TestRunReceiptLifecycleHookIsEnforcedByRegistration(t *testing.T) {
	t.Parallel()
	completed := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		code, want string
		wantError  bool
	}{
		{"executor_fault", `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":"2026-10-21T12:00:00Z","failure_code":"executor_fault"}`, false},
		{"", `{"failure_code":"internal_error","message":"internal error"}`, true},
	} {
		t.Run("failure_code="+test.code, func(t *testing.T) {
			t.Parallel()
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
			mcptools.Register(server, mcptools.Tool[struct{}, RunReceipt]{
				Name: "receipt",
				Handler: func(context.Context, struct{}) (RunReceipt, error) {
					return RunReceipt{
						AutomationID: "aut",
						RunID:        "run",
						Status:       "failed",
						CompletedAt:  &completed,
						FailureCode:  test.code,
					}, nil
				},
			})
			request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"receipt","arguments":{}}}`,
			))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
			response := httptest.NewRecorder()
			server.HTTPHandler().ServeHTTP(response, request)
			_, data, found := strings.Cut(response.Body.String(), "data: ")
			if response.Code != http.StatusOK || !found {
				t.Fatalf("response = %d: %s", response.Code, response.Body.String())
			}
			var envelope struct {
				Result struct {
					IsError    bool            `json:"isError"`
					Structured json.RawMessage `json:"structuredContent"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Result.IsError != test.wantError || string(envelope.Result.Structured) != test.want {
				t.Fatalf("receipt = %s, want isError=%t body=%s", data, test.wantError, test.want)
			}
		})
	}
}
