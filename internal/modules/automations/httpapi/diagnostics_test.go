package httpapi //nolint:testpackage // Tests exercise the actual HTTP failure boundary.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestAutomationHTTPDiagnosticsOwnUnexpectedFailures(t *testing.T) {
	t.Parallel()
	const id = "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	for _, test := range []struct {
		name, method, path       string
		cause                    error
		canceled, wantDiagnostic bool
		status                   int
	}{
		{"read", "GET", "/automations/" + id, errors.New("SENSITIVE-automation"), false, true, 500},
		{"manual admission", "POST", "/automations/" + id + "/runs", errors.New("SENSITIVE-admission"), false, true, 500},
		{"history mapping", "GET", "/automations/" + id + "/history/arn_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil, false, true, 500},
		{"caller cancellation", "GET", "/automations/" + id, context.Canceled, true, false, 500},
		{"dependency deadline", "GET", "/automations/" + id, context.DeadlineExceeded, false, true, 500},
		{"joined fault", "GET", "/automations/" + id, errors.Join(context.Canceled, errors.New("SENSITIVE-storage")), true, true, 500},
		{"modeled not found", "GET", "/automations/" + id, automations.ErrAutomationNotFound, false, false, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Evidence", "1"))
			Register(api, &diagnosticAutomationStub{cause: test.cause}, slog.New(slog.NewJSONHandler(&logs, nil)))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}
			request := httptest.NewRequestWithContext(ctx, test.method, test.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String()+fmt.Sprint(response.Header())+logs.String(), "SENSITIVE") {
				t.Fatal("private cause leaked")
			}
			if test.wantDiagnostic {
				var record map[string]any
				if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
					t.Errorf("want exactly one safe diagnostic: %v", err)
				} else {
					assertAutomationDiagnosticFields(t, record, test.name)
				}
			} else if logs.Len() != 0 {
				t.Errorf("expected silence, got %s", logs.String())
			}
		})
	}
}

func assertAutomationDiagnosticFields(t *testing.T, record map[string]any, name string) {
	t.Helper()
	operation, stage := "get-automation", "service"
	if name == "manual admission" {
		operation = "start-automation-run"
	}
	if name == "history mapping" {
		operation, stage = "get-automation-history-entry", "mapping"
	}
	if record["level"] != "ERROR" || record["event"] != "automation.http_internal_failure" ||
		record["operation"] != operation || record["stage"] != stage || record["error_code"] != "internal_error" ||
		record["error_type"] == "" || len(record) != 8 {
		t.Errorf("unexpected diagnostic fields: %v", record)
	}
}

type diagnosticAutomationStub struct {
	Automations

	cause error
}

func (stub *diagnosticAutomationStub) GetAutomation(
	context.Context,
	automations.AutomationID,
) (automations.Record, error) {
	return automations.Record{}, stub.cause
}

func (stub *diagnosticAutomationStub) StartManualRun(
	context.Context,
	automations.ManualRunInput,
) (automations.Run, error) {
	return automations.Run{}, stub.cause
}

func (*diagnosticAutomationStub) GetHistoryEntry(
	context.Context,
	automations.AutomationID,
	string,
) (automations.HistoryEntry, error) {
	// Supply malformed service output to reach the handler's existing safe
	// invalid_history_evidence branch without changing persistence behavior.
	return nil, nil //nolint:nilnil // Intentionally malformed service output reaches the response mapper.
}
