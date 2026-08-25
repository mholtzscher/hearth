package hearthd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

type testEntityReader struct {
	view devices.EntityView
}

func (reader *testEntityReader) GetEntity(context.Context, devices.EntityID) (devices.EntityView, error) {
	return reader.view, nil
}

type testCommandExecutor struct{}

func (*testCommandExecutor) ExecuteCommand(
	context.Context,
	devices.EntityID,
	devices.OperationName,
	devices.CommandParameters,
) (devices.CommandResult, error) {
	return devices.CommandResult{}, errors.New("unexpected ExecuteCommand call")
}

func TestHTTPHandlerServesHealthReadinessAndDeviceOperations(t *testing.T) {
	reader := &testEntityReader{view: devices.EntityView{Entity: devices.Entity{
		ID: testHTTPEntityID, DeviceID: testHTTPDeviceID, AdapterID: "simulator", Name: "Power",
		TypeID: devices.EntityTypePowerV1, Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
	}}}
	readiness := &testReadiness{}
	handler, api := NewHTTPHandler(devicesapi.Dependencies{Entities: reader, Commands: &testCommandExecutor{}}, readiness)

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
	reader := &testEntityReader{view: devices.EntityView{Entity: devices.Entity{
		ID: testHTTPEntityID, DeviceID: testHTTPDeviceID, AdapterID: "simulator", Name: "Power",
		TypeID: devices.EntityTypePowerV1, Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
	}}}
	handler, _ := NewHTTPHandler(devicesapi.Dependencies{Entities: reader, Commands: &testCommandExecutor{}}, &testReadiness{})
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
