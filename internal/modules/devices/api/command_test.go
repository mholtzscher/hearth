package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const apiCommandID = devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")

func TestExecuteCommandReturnsSatisfiedResultAndRegistersOpenAPI(t *testing.T) {
	t.Parallel()
	var requestedEntityID devices.EntityID
	var requestedOperation devices.OperationName
	var requestedParameters devices.CommandParameters
	stub := &stubDevices{executeCommand: func(
		_ context.Context,
		entityID devices.EntityID,
		operation devices.OperationName,
		parameters devices.CommandParameters,
	) (devices.CommandResult, error) {
		requestedEntityID = entityID
		requestedOperation = operation
		requestedParameters = append(devices.CommandParameters(nil), parameters...)
		observationID := apiObservationID
		value := devices.Value(`true`)
		return devices.CommandResult{
			CommandID: apiCommandID, Outcome: devices.OutcomeObserved,
			ObservationID: &observationID, Value: &value,
		}, nil
	}}
	router, openapi := testAPI(t, stub)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/entities/"+string(apiEntityID)+"/commands",
		bytes.NewBufferString(`{"operation":"set","parameters":{"value":true}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if requestedEntityID != apiEntityID || requestedOperation != devices.OperationName("set") ||
		string(requestedParameters) != `{"value":true}` {
		t.Fatalf("ExecuteCommand arguments = %q, %q, %s", requestedEntityID, requestedOperation, requestedParameters)
	}
	var body CommandResultBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CommandID != string(apiCommandID) || body.Status != "satisfied" ||
		body.ObservationID == nil || *body.ObservationID != string(apiObservationID) ||
		body.Value == nil || *body.Value != true {
		t.Fatalf("body = %#v", body)
	}
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}/commands"].Post
	if operation == nil || operation.OperationID != "execute-entity-command" ||
		operation.Summary != "Execute an Entity Command" || len(operation.Tags) != 1 || operation.Tags[0] != "Entities" {
		t.Fatalf("POST operation = %#v", operation)
	}
	if _, ok := operation.Responses["422"]; !ok {
		t.Fatalf("POST operation is missing standard 422 response: %#v", operation.Responses)
	}
}

func TestExecuteCommandReturnsDispatchedResultWithoutEvidenceFields(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{executeCommand: func(
		context.Context,
		devices.EntityID,
		devices.OperationName,
		devices.CommandParameters,
	) (devices.CommandResult, error) {
		result, err := devices.NewCommandResult(apiCommandID, devices.OutcomeDispatched, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result, nil
	}}
	router, _ := testAPI(t, stub)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/entities/"+string(apiEntityID)+"/commands",
		bytes.NewBufferString(`{"operation":"trigger","parameters":{"name":"blink"}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["command_id"] != string(apiCommandID) || raw["status"] != "dispatched" {
		t.Fatalf("body = %s", response.Body.String())
	}
	// Dispatched results omit both evidence fields instead of rendering null.
	if _, present := raw["observation_id"]; present {
		t.Fatalf("dispatched body carries observation_id: %s", response.Body.String())
	}
	if _, present := raw["value"]; present {
		t.Fatalf("dispatched body carries value: %s", response.Body.String())
	}
	var body CommandResultBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CommandID != string(apiCommandID) || body.Status != "dispatched" ||
		body.ObservationID != nil || body.Value != nil {
		t.Fatalf("body = %#v", body)
	}
}

func TestExecuteDisabledCommandReturnsDurableProblemDetailsExtension(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{executeCommand: func(
		context.Context,
		devices.EntityID,
		devices.OperationName,
		devices.CommandParameters,
	) (devices.CommandResult, error) {
		return devices.CommandResult{}, &devices.CommandExecutionError{
			CommandID: apiCommandID, Err: devices.ErrEntityDisabled,
		}
	}}
	router, _ := testAPI(t, stub)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/entities/"+string(apiEntityID)+"/commands",
		bytes.NewBufferString(`{"operation":"set","parameters":{"value":true}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf(
			"status/content type = %d/%q, body = %s",
			response.Code,
			response.Header().Get("Content-Type"),
			response.Body.String(),
		)
	}
	var problem disabledCommandError
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Type != "about:blank" || problem.Title != "Conflict" || problem.Status != http.StatusConflict ||
		problem.Detail != "entity is disabled" || problem.Code != "entity_disabled" ||
		problem.CommandID != string(apiCommandID) {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestCommandErrorMappingUsesStandardHumaErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cause  error
		status int
		detail string
	}{
		{devices.ErrInvalidCommand, http.StatusBadRequest, "invalid command"},
		{devices.ErrEntityNotFound, http.StatusNotFound, "entity not found"},
		{devices.ErrAdapterUnhealthy, http.StatusServiceUnavailable, "adapter unhealthy"},
		{devices.ErrEntityUnavailable, http.StatusServiceUnavailable, "entity unavailable"},
		{devices.ErrUpstreamRejected, http.StatusBadGateway, "upstream rejected command"},
		{devices.ErrOutcomeTimeout, http.StatusGatewayTimeout, "command outcome timed out"},
		{errors.New("SQLite unavailable"), http.StatusInternalServerError, "internal error"},
	}
	for _, test := range tests {
		err := mapCommandError(&devices.CommandExecutionError{CommandID: apiCommandID, Err: test.cause})
		var status huma.StatusError
		ok := errors.As(err, &status)
		if !ok || status.GetStatus() != test.status || status.Error() != test.detail {
			t.Fatalf("mapped %v = %#v", test.cause, status)
		}
	}
}
