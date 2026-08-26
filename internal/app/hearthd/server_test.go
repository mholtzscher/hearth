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
	getEntity      func(context.Context, devices.EntityID) (devices.EntityWithState, error)
	executeCommand func(
		context.Context,
		devices.EntityID,
		devices.OperationName,
		devices.CommandParameters,
	) (devices.CommandResult, error)
}

func (stub *stubDevices) GetEntity(ctx context.Context, entityID devices.EntityID) (devices.EntityWithState, error) {
	if stub.getEntity == nil {
		panic("unexpected GetEntity call")
	}
	return stub.getEntity(ctx, entityID)
}

func (*stubDevices) ListDevices(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error) {
	panic("unexpected ListDevices call")
}

func (*stubDevices) GetDevice(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error) {
	panic("unexpected GetDevice call")
}

func (*stubDevices) ListEntities(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
	panic("unexpected ListEntities call")
}

func (*stubDevices) GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error) {
	panic("unexpected GetCommand call")
}

func (*stubDevices) ListEntityCommands(context.Context, devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error) {
	panic("unexpected ListEntityCommands call")
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
	stub := &stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
		return devices.EntityWithState{Entity: devices.Entity{
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
	if len(document.Paths) != 6 {
		t.Fatalf("OpenAPI paths = %v", document.Paths)
	}
	assertRuntimeOpenAPIOperation(t, document.Paths["/v1/entities"].Get, "list-entities", "200", "400", "422", "500")
	getEntity := document.Paths["/v1/entities/{entity_id}"].Get
	assertRuntimeOpenAPIOperation(t, getEntity, "get-entity", "200", "400", "404", "422", "500")
	executeCommand := document.Paths["/v1/entities/{entity_id}/commands"].Post
	assertRuntimeOpenAPIOperation(t, executeCommand, "execute-entity-command", "200", "400", "404", "422", "502", "503", "504", "500")
	assertRuntimeOpenAPIOperation(t, document.Paths["/v1/entities/{entity_id}/commands"].Get, "list-entity-commands", "200", "400", "404", "422", "500")
	assertRuntimeOpenAPIOperation(t, document.Paths["/v1/devices"].Get, "list-devices", "200", "400", "422", "500")
	assertRuntimeOpenAPIOperation(t, document.Paths["/v1/devices/{device_id}"].Get, "get-device", "200", "400", "404", "422", "500")
	assertRuntimeOpenAPIOperation(t, document.Paths["/v1/commands/{command_id}"].Get, "get-command", "200", "400", "404", "422", "500")
	if executeCommand.RequestBody == nil || !executeCommand.RequestBody.Required {
		t.Fatalf("command request body = %#v", executeCommand.RequestBody)
	}

	for schemaName, properties := range map[string][]string{
		"EntityBody":           {"id", "device_id", "name", "type", "support", "state"},
		"StateBody":            {"value", "observation_id", "adapter_received_at", "source_updated_at", "observed_at"},
		"CommandBody":          {"operation", "parameters"},
		"CommandResultBody":    {"command_id", "status", "observation_id", "value"},
		"DeviceBody":           {"id", "kind", "name"},
		"DeviceDetailBody":     {"id", "kind", "name", "entities", "next_entity_cursor"},
		"EntityCollectionBody": {"items", "next_cursor"},
		"DeviceCollectionBody": {"items", "next_cursor"},
		"CommandRecordBody": {
			"id", "entity_id", "operation", "parameters", "status", "requested_at", "deadline_at",
			"accepted_at", "completed_at", "outcome_observation_id", "failure_code",
		},
		"CommandCollectionBody": {"items", "next_cursor"},
		"ErrorModel":            {"type", "title", "status", "detail", "instance", "errors"},
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

func TestHTTPHandlerUsesStandardHumaValidationErrors(t *testing.T) {
	handler, _ := NewHTTPHandler(&stubDevices{}, nil)
	for _, test := range []struct {
		body   string
		status int
	}{{`{`, http.StatusBadRequest}, {`{"operation":"set"}`, http.StatusUnprocessableEntity}} {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/entities/"+string(testHTTPEntityID)+"/commands",
			bytes.NewBufferString(test.body),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("body %q: status = %d, response = %s", test.body, response.Code, response.Body.String())
		}
		var errorBody huma.ErrorModel
		if err := json.Unmarshal(response.Body.Bytes(), &errorBody); err != nil {
			t.Fatal(err)
		}
		if errorBody.Status != test.status {
			t.Fatalf("body %q: error = %#v", test.body, errorBody)
		}
	}
}

func TestNewHTTPHandlerPreservesHumaErrorFactory(t *testing.T) {
	original := huma.NewError
	called := false
	huma.NewError = func(status int, message string, details ...error) huma.StatusError {
		called = true
		return original(status, message, details...)
	}
	t.Cleanup(func() { huma.NewError = original })

	NewHTTPHandler(&stubDevices{}, nil)
	called = false
	err := huma.NewError(http.StatusTeapot, "teapot")
	if !called || err.GetStatus() != http.StatusTeapot || err.Error() != "teapot" {
		t.Fatalf("NewHTTPHandler changed huma.NewError behavior: %#v", err)
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
		if status != "200" && !strings.Contains(string(response), "#/components/schemas/ErrorModel") {
			t.Errorf("OpenAPI operation %q response %s does not use Huma's standard error body: %s", operationID, status, response)
		}
	}
}

func appRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
