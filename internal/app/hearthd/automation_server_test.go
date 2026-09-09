package hearthd //nolint:testpackage // App tests verify the actual runtime HTTP assembly and readiness gate.

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"time"

	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

type runtimeAutomationOperation struct {
	OperationID string `json:"operationId"`
	RequestBody *struct {
		Required bool `json:"required"`
		Content  map[string]struct {
			Schema struct {
				Ref string `json:"$ref"`
			} `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Parameters []struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
		Schema   struct {
			Type    string          `json:"type"`
			Minimum int             `json:"minimum"`
			Maximum int             `json:"maximum"`
			Default json.RawMessage `json:"default"`
		} `json:"schema"`
	} `json:"parameters"`
	Responses map[string]json.RawMessage `json:"responses"`
}

// A10/A11: the runtime document, not typed Huma metadata, is the public contract.
//
//nolint:gocognit // Each operation has distinct schema and required-parameter contracts.
func TestAutomationRuntimeOpenAPIContract(t *testing.T) {
	t.Parallel()
	codec := testHTTPAutomationCodec(t)
	handler, _ := NewHTTPHandler(&stubDevices{}, &stubHTTPAutomations{}, codec, &testReadiness{}, &stubDevices{})
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var document struct {
		Paths      map[string]map[string]runtimeAutomationOperation `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	canonical := decodeRuntimeAutomationSchema(t, codec.AutomationDefinitionSchema())
	published := decodeRuntimeAutomationSchema(t, document.Components.Schemas["AutomationDefinition"])
	if !reflect.DeepEqual(canonical, published) {
		t.Fatalf("canonical definition schema changed: %#v", published)
	}
	routes := []struct{ path, method, id, success string }{
		{"/v1/automations", "post", "create-automation", "201"},
		{"/v1/automations", "get", "list-automations", "200"},
		{"/v1/automations/{automation_id}", "get", "get-automation", "200"},
		{"/v1/automations/{automation_id}", "put", "update-automation", "200"},
		{"/v1/automations/{automation_id}", "delete", "delete-automation", "204"},
		{"/v1/automations/{automation_id}/runs", "post", "start-automation-run", "202"},
		{"/v1/automation-runs", "get", "list-automation-runs", "200"},
		{"/v1/automation-runs/{run_id}", "get", "get-automation-run", "200"},
	}
	for _, route := range routes {
		operation := document.Paths[route.path][route.method]
		if operation.OperationID != route.id {
			t.Fatalf("%s %s operation ID = %q", route.method, route.path, operation.OperationID)
		}
		for _, status := range []string{route.success, "400", "404", "409", "422", "500", "503"} {
			if len(operation.Responses[status]) == 0 {
				t.Fatalf("%s missing response %s", route.id, status)
			}
		}
		if route.id == "create-automation" || route.id == "update-automation" {
			if operation.RequestBody == nil || !operation.RequestBody.Required ||
				operation.RequestBody.Content["application/json"].Schema.Ref != "#/components/schemas/AutomationDefinition" {
				t.Fatalf("%s does not reference required canonical definition: %#v", route.id, operation.RequestBody)
			}
		}
		if route.id == "start-automation-run" {
			if len(operation.Responses["200"]) == 0 || operation.RequestBody != nil {
				t.Fatal("manual invocation must document reuse and accept no body")
			}
			requireRuntimeAutomationParameter(t, operation, "Idempotency-Key", "header", "string", true)
		}
		if route.method == "put" || route.method == "delete" {
			requireRuntimeAutomationParameter(t, operation, "expected_revision", "query", "integer", true)
		}
		if route.id == "list-automations" || route.id == "list-automation-runs" {
			requireRuntimeAutomationParameter(t, operation, "limit", "query", "integer", false)
		}
	}
	for name, schema := range document.Components.Schemas {
		if bytes.Contains(schema, []byte("reserved_correlation_id")) ||
			bytes.Contains(schema, []byte("idempotency_key")) {
			t.Fatalf("internal evidence exposed by schema %s", name)
		}
	}
}
func decodeRuntimeAutomationSchema(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func requireRuntimeAutomationParameter(
	t *testing.T,
	operation runtimeAutomationOperation,
	name, in, schemaType string,
	required bool,
) {
	t.Helper()
	for _, parameter := range operation.Parameters {
		if parameter.Name != name {
			continue
		}
		if parameter.In != in || parameter.Required != required || parameter.Schema.Type != schemaType {
			t.Fatalf("%s %s parameter = %#v", operation.OperationID, name, parameter)
		}
		if name == "expected_revision" && parameter.Schema.Minimum != 1 {
			t.Fatal("revision must be positive")
		}
		if name == "limit" &&
			(parameter.Schema.Minimum != 1 || parameter.Schema.Maximum != 200 || string(parameter.Schema.Default) != "50") {
			t.Fatal("pagination bounds differ from contract")
		}
		return
	}
	t.Fatalf("%s missing %s", operation.OperationID, name)
}

func TestAutomationHTTPReadinessGate(t *testing.T) {
	t.Parallel()
	service := &stubHTTPAutomations{}
	handler, _ := NewHTTPHandler(&stubDevices{}, service, testHTTPAutomationCodec(t), &testReadiness{}, &stubDevices{})
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusOK {
		t.Fatal("open healthy admission must be ready")
	}
	service.unavailable = true
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatal("closed/faulted automation admission must degrade readiness")
	}
	if response := appRequest(handler, "/healthz"); response.Code != http.StatusOK {
		t.Fatal("executor fault must not affect liveness")
	}
}

// automationFaultCommands simulates ambiguous execution with unreadable evidence.
type automationFaultCommands struct{}

func (automationFaultCommands) ValidateCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandParameters, error) {
	return input.Parameters, nil
}
func (automationFaultCommands) ExecuteAutomationStepCommand(
	context.Context,
	devices.CommandInput,
) (devices.CommandResult, error) {
	return devices.CommandResult{}, errors.New("private execution details")
}
func (automationFaultCommands) GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error) {
	return devices.CommandRecord{}, errors.New("private SQL read failure")
}

func TestAutomationExecutorFaultDegradesHTTPReadiness(t *testing.T) {
	t.Parallel()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "fault.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if err = platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	codec := testHTTPAutomationCodec(t)
	repo := automations.NewSQLiteRepository(database)
	service := automations.NewService(repo, automationFaultCommands{}, automationFaultCommands{}, codec, time.UTC, nil)
	t.Cleanup(func() {
		service.StopAutomationExecutionAdmission()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if waitErr := service.WaitAutomationRuns(ctx); waitErr != nil {
			t.Error(waitErr)
		}
	})
	definition, err := codec.DecodeAutomationDefinition(
		[]byte(
			`{"name":"Fault","triggers":[{"id":"daily","kind":"cron","expression":"0 19 * * *"}],"steps":[{"entity_id":"ent_01900000-0000-7000-8000-000000000001","operation_name":"set","parameters":{"value":true}}]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := NewHTTPHandler(&stubDevices{}, service, codec, &testReadiness{}, &stubDevices{})
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusOK {
		t.Fatal("healthy service was not ready")
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/automations/"+string(record.ID)+"/runs", nil)
	request.Header.Set("Idempotency-Key", "fault-secret-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("admission = %d %s", response.Code, response.Body.String())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err = service.WaitAutomationRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if readiness := appRequest(handler, "/readyz"); readiness.Code != http.StatusServiceUnavailable {
		t.Fatal("latched executor fault did not degrade HTTP readiness")
	}
	detail := appRequest(handler, response.Header().Get("Location"))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"status":"running"`) {
		t.Fatalf("uncertain Run became terminal: %s", detail.Body.String())
	}
	for _, secret := range []string{"private", "SQL", "fault-secret-key", "cor_"} {
		if strings.Contains(detail.Body.String(), secret) {
			t.Fatalf("faulted history leaked %q", secret)
		}
	}
	service.StopAutomationExecutionAdmission()
	if err = service.WaitAutomationRuns(ctx); err != nil {
		t.Fatal(err)
	}
	afterStop := appRequest(handler, response.Header().Get("Location"))
	if afterStop.Code != http.StatusOK || !strings.Contains(afterStop.Body.String(), `"status":"running"`) {
		t.Fatal("shutdown overrode uncertain fault retention")
	}
}
