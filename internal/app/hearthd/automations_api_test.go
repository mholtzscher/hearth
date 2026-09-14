package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// stubAutomations is the narrow HTTP seam the runtime handler is assembled
// with. Every definition or history operation panics: the app tests here assert
// transport registration and the admission readiness gate, never automation
// behavior, which the automations module owns and tests through its own Huma
// registration.
type stubAutomations struct {
	admissionClosed bool
}

func (*stubAutomations) CreateAutomation(
	context.Context,
	automations.AutomationDefinition,
) (automations.AutomationRecord, error) {
	panic("unexpected CreateAutomation call")
}

func (*stubAutomations) GetAutomation(
	context.Context,
	automations.AutomationID,
) (automations.AutomationRecord, error) {
	panic("unexpected GetAutomation call")
}

func (*stubAutomations) ListAutomations(
	context.Context,
	automations.ListAutomationsParams,
) (automations.AutomationPage[automations.AutomationRecord], error) {
	panic("unexpected ListAutomations call")
}

func (*stubAutomations) ReplaceAutomation(
	context.Context,
	automations.AutomationID,
	int64,
	automations.AutomationDefinition,
) (automations.AutomationRecord, error) {
	panic("unexpected ReplaceAutomation call")
}

func (*stubAutomations) DeleteAutomation(context.Context, automations.AutomationID, int64) error {
	panic("unexpected DeleteAutomation call")
}

func (*stubAutomations) StartManualRun(
	context.Context,
	automations.AutomationID,
) (automations.AutomationRun, error) {
	panic("unexpected StartManualRun call")
}

func (*stubAutomations) GetHistoryEntry(
	context.Context,
	automations.AutomationID,
	string,
) (automations.AutomationHistoryEntry, error) {
	panic("unexpected GetHistoryEntry call")
}

func (*stubAutomations) ListHistory(
	context.Context,
	automations.ListHistoryParams,
) (automations.AutomationPage[automations.AutomationHistorySummary], error) {
	panic("unexpected ListHistory call")
}

func (stub *stubAutomations) AdmissionOpen() bool {
	return stub == nil || !stub.admissionClosed
}

// TestRuntimeExposesAutomationOperations protects the app-level HTTP surface:
// the runtime must register all eight automation operations from section 8.4 as
// one Huma-backed group so the published OpenAPI document carries them. It fails
// if registration is dropped or an operation is renamed.
func TestRuntimeExposesAutomationOperations(t *testing.T) {
	t.Parallel()
	handler, _ := NewHTTPHandler(
		&stubDevices{},
		&stubAutomations{},
		&testReadiness{},
		&stubDevices{},
		&stubAutomations{},
	)
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d, body = %s", response.Code, response.Body.String())
	}
	var document struct {
		Paths map[string]map[string]struct {
			OperationID string   `json:"operationId"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}

	expected := []struct {
		method      string
		path        string
		operationID string
	}{
		{"post", "/v1/automations", "create-automation"},
		{"get", "/v1/automations", "list-automations"},
		{"get", "/v1/automations/{automation_id}", "get-automation"},
		{"put", "/v1/automations/{automation_id}", "replace-automation"},
		{"delete", "/v1/automations/{automation_id}", "delete-automation"},
		{"post", "/v1/automations/{automation_id}/runs", "start-automation-run"},
		{"get", "/v1/automations/{automation_id}/history", "list-automation-history"},
		{"get", "/v1/automations/{automation_id}/history/{entry_id}", "get-automation-history-entry"},
	}
	for _, want := range expected {
		pathItem, ok := document.Paths[want.path]
		if !ok {
			t.Fatalf("OpenAPI is missing automation path %q", want.path)
		}
		operation, ok := pathItem[want.method]
		if !ok || operation.OperationID != want.operationID {
			t.Fatalf(
				"OpenAPI %s %s = %#v, want operation %q",
				want.method, want.path, operation, want.operationID,
			)
		}
		if len(operation.Tags) != 1 || operation.Tags[0] != "Automations" {
			t.Fatalf(
				"OpenAPI %s %s tags = %v, want [Automations]",
				want.method, want.path, operation.Tags,
			)
		}
	}
	for _, schema := range []string{"AutomationDefinition", "AutomationBody", "AutomationCollectionBody"} {
		if _, ok := document.Components.Schemas[schema]; !ok {
			t.Errorf("OpenAPI is missing automation schema %q", schema)
		}
	}
}

// TestRuntimeReadinessRequiresOpenAutomationAdmission protects A13 at the
// transport surface: readiness fails when automation admission has closed even
// though every broker and persistence dependency is healthy, and health stays
// green. It fails if a closed gate still reports ready.
func TestRuntimeReadinessRequiresOpenAutomationAdmission(t *testing.T) {
	t.Parallel()
	admission := &stubAutomations{}
	handler, _ := NewHTTPHandler(
		&stubDevices{},
		&stubAutomations{},
		&testReadiness{},
		&stubDevices{},
		admission,
	)
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", response.Code, response.Body.String())
	}
	admission.admissionClosed = true
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with closed automation admission = %d", response.Code)
	}
	if response := appRequest(handler, "/healthz"); response.Code != http.StatusOK {
		t.Fatalf("healthz with closed automation admission = %d", response.Code)
	}
}
