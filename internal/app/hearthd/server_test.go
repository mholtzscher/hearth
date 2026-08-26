package hearthd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
)

const (
	testHTTPDeviceID = devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	testHTTPEntityID = devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

type testReadiness struct {
	err error
}

func (readiness *testReadiness) Check(context.Context) error {
	return readiness.err
}

type stubDevices struct {
	getEntity      func(context.Context, devices.EntityID) (devices.EntityView, error)
	executeCommand func(
		context.Context,
		devices.EntityID,
		devices.OperationName,
		devices.CommandParameters,
	) (devices.CommandResult, error)
}

func (stub *stubDevices) GetEntity(ctx context.Context, entityID devices.EntityID) (devices.EntityView, error) {
	if stub.getEntity == nil {
		panic("unexpected GetEntity call")
	}
	return stub.getEntity(ctx, entityID)
}

func (stub *stubDevices) ExecuteCommand(
	ctx context.Context,
	entityID devices.EntityID,
	operation devices.OperationName,
	parameters devices.CommandParameters,
) (devices.CommandResult, error) {
	if stub.executeCommand == nil {
		panic("unexpected ExecuteCommand call")
	}
	return stub.executeCommand(ctx, entityID, operation, parameters)
}

func TestHTTPHandlerServesHealthReadinessAndDeviceOperations(t *testing.T) {
	stub := &stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityView, error) {
		return devices.EntityView{Entity: devices.Entity{
			ID: testHTTPEntityID, DeviceID: testHTTPDeviceID, AdapterID: "simulator", Name: "Power",
			TypeID: devices.EntityTypePowerV1, Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		}}, nil
	}}
	readiness := &testReadiness{}
	handler, api := NewHTTPHandler(stub, readiness)

	if response := appRequest(handler, "/healthz"); response.Code != http.StatusOK {
		t.Fatalf("health status = %d", response.Code)
	}
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusOK {
		t.Fatalf("ready status = %d", response.Code)
	}
	readiness.err = errors.New("NATS disconnected")
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", response.Code)
	}
	if response := appRequest(handler, "/healthz"); response.Code != http.StatusOK {
		t.Fatalf("health status while unready = %d", response.Code)
	}
	if response := appRequest(handler, "/v1/entities/"+string(testHTTPEntityID)); response.Code != http.StatusOK {
		t.Fatalf("entity status = %d, body = %s", response.Code, response.Body.String())
	}
	operation := api.OpenAPI().Paths["/v1/entities/{entity_id}"].Get
	if operation == nil || operation.OperationID != "get-entity" {
		t.Fatalf("GET operation = %#v", operation)
	}
	commandOperation := api.OpenAPI().Paths["/v1/entities/{entity_id}/commands"].Post
	if commandOperation == nil || commandOperation.OperationID != "execute-entity-command" {
		t.Fatalf("POST operation = %#v", commandOperation)
	}
}

func TestRuntimeOpenAPIContract(t *testing.T) {
	handler, _ := NewHTTPHandler(&stubDevices{}, &testReadiness{})
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/openapi+json" {
		t.Fatalf("OpenAPI content type = %q", contentType)
	}

	var document struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]struct {
			Get  *runtimeOpenAPIOperation `json:"get"`
			Post *runtimeOpenAPIOperation `json:"post"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "3.1.0" || document.Info.Title != "Hearth" || document.Info.Version != "1.0.0" {
		t.Fatalf("OpenAPI metadata = %#v", document)
	}
	if len(document.Paths) != 2 {
		t.Fatalf("OpenAPI paths = %v", document.Paths)
	}
	getEntity := document.Paths["/v1/entities/{entity_id}"].Get
	assertRuntimeOpenAPIOperation(t, getEntity, "get-entity", "200", "400", "404", "500")
	executeCommand := document.Paths["/v1/entities/{entity_id}/commands"].Post
	assertRuntimeOpenAPIOperation(t, executeCommand, "execute-entity-command", "200", "400", "404", "502", "503", "504", "500")
	if executeCommand.RequestBody == nil || !executeCommand.RequestBody.Required {
		t.Fatalf("command request body = %#v", executeCommand.RequestBody)
	}

	for schemaName, properties := range map[string][]string{
		"EntityBody":        {"id", "device_id", "name", "type", "support", "state"},
		"StateBody":         {"value", "observation_id", "adapter_received_at", "source_updated_at", "observed_at"},
		"CommandBody":       {"operation", "parameters"},
		"CommandResultBody": {"command_id", "status", "observation_id", "value"},
		"StatusError":       {"error"},
		"APIError":          {"code", "message", "command_id"},
	} {
		raw, ok := document.Components.Schemas[schemaName]
		if !ok {
			t.Fatalf("OpenAPI is missing %s schema", schemaName)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		for _, property := range properties {
			if _, ok := schema.Properties[property]; !ok {
				t.Errorf("OpenAPI %s schema is missing %q", schemaName, property)
			}
		}
	}
	var stateSchema struct {
		Type []string `json:"type"`
	}
	if err := json.Unmarshal(document.Components.Schemas["StateBody"], &stateSchema); err != nil {
		t.Fatal(err)
	}
	var nullable bool
	for _, schemaType := range stateSchema.Type {
		if schemaType == "null" {
			nullable = true
		}
	}
	if !nullable {
		t.Fatalf("OpenAPI StateBody is not nullable: %s", document.Components.Schemas["StateBody"])
	}
}

func TestHTTPHandlerNormalizesMalformedCommandRequests(t *testing.T) {
	handler, _ := NewHTTPHandler(&stubDevices{}, nil)
	for _, body := range []string{`{`, `{"operation":"set"}`} {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/entities/"+string(testHTTPEntityID)+"/commands",
			bytes.NewBufferString(body),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, response = %s", body, response.Code, response.Body.String())
		}
		var errorBody devicesapi.ErrorBody
		if err := json.Unmarshal(response.Body.Bytes(), &errorBody); err != nil {
			t.Fatal(err)
		}
		if errorBody.Error.Code != "invalid_request" || errorBody.Error.Message != "invalid request" {
			t.Fatalf("body %q: error = %#v", body, errorBody.Error)
		}
	}
}

func TestNewHTTPHandlerInstallsHumaErrorPolicy(t *testing.T) {
	original := huma.NewError
	sentinelCalled := false
	huma.NewError = func(status int, message string, _ ...error) huma.StatusError {
		sentinelCalled = true
		return devicesapi.NewStatusError(status, "sentinel", message)
	}
	t.Cleanup(func() { huma.NewError = original })

	NewHTTPHandler(&stubDevices{}, nil)
	sentinelCalled = false
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity,
	} {
		assertHumaError(t, huma.NewError(status, "validation failed"), http.StatusBadRequest, "invalid_request", "invalid request")
	}
	if sentinelCalled {
		t.Fatal("NewHTTPHandler did not replace huma.NewError")
	}
	assertHumaError(t, huma.NewError(0, "schema error"), 0, "internal_error", "schema error")

	fallback := huma.NewError(http.StatusTeapot, "teapot")
	wantFallback := defaultHumaNewError(http.StatusTeapot, "teapot")
	if fallback.GetStatus() != wantFallback.GetStatus() || fallback.Error() != wantFallback.Error() {
		t.Fatalf("fallback = %#v, want %#v", fallback, wantFallback)
	}
}

func assertHumaError(t *testing.T, err huma.StatusError, status int, code, message string) {
	t.Helper()
	if err.GetStatus() != status {
		t.Fatalf("status = %d, want %d", err.GetStatus(), status)
	}
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var body devicesapi.ErrorBody
	if unmarshalErr := json.Unmarshal(encoded, &body); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if body.Error.Code != code || body.Error.Message != message {
		t.Fatalf("error = %#v", body.Error)
	}
}

type runtimeOpenAPIOperation struct {
	OperationID string                     `json:"operationId"`
	RequestBody *runtimeOpenAPIRequestBody `json:"requestBody"`
	Responses   map[string]json.RawMessage `json:"responses"`
}

type runtimeOpenAPIRequestBody struct {
	Required bool `json:"required"`
}

func assertRuntimeOpenAPIOperation(t *testing.T, operation *runtimeOpenAPIOperation, operationID string, statuses ...string) {
	t.Helper()
	if operation == nil || operation.OperationID != operationID {
		t.Fatalf("OpenAPI operation = %#v, want %q", operation, operationID)
	}
	if len(operation.Responses) != len(statuses) {
		t.Errorf("OpenAPI operation %q responses = %v, want %v", operationID, operation.Responses, statuses)
	}
	for _, status := range statuses {
		response, ok := operation.Responses[status]
		if !ok {
			t.Errorf("OpenAPI operation %q is missing response %s", operationID, status)
			continue
		}
		if status != "200" && !strings.Contains(string(response), "#/components/schemas/StatusError") {
			t.Errorf("OpenAPI operation %q response %s does not use the stable error body: %s", operationID, status, response)
		}
	}
}

func appRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
