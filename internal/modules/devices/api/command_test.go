package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const apiCommandID = devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")

func TestExecuteCommandReturnsSatisfiedResultAndRegistersOpenAPI(t *testing.T) {
	executor := &stubCommandExecutor{result: devices.CommandResult{
		CommandID: apiCommandID, ObservationID: apiObservationID, Value: devices.Value(`true`),
	}}
	router, openapi := testAPI(t, &stubEntityReader{}, executor)
	request := httptest.NewRequest(
		http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/commands",
		bytes.NewBufferString(`{"operation":"set","parameters":{"value":true}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body CommandResultBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CommandID != string(apiCommandID) || body.Status != "satisfied" ||
		body.ObservationID != string(apiObservationID) || body.Value != true {
		t.Fatalf("body = %#v", body)
	}
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}/commands"].Post
	if operation == nil || operation.OperationID != "execute-entity-command" {
		t.Fatalf("POST operation = %#v", operation)
	}
}

func TestExecuteCommandMapsMalformedBodiesToStableBadRequest(t *testing.T) {
	router, _ := testAPI(t, &stubEntityReader{}, &stubCommandExecutor{})
	for _, body := range []string{`{`, `{"operation":"set"}`} {
		request := httptest.NewRequest(
			http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/commands", bytes.NewBufferString(body),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, response = %s", body, response.Code, response.Body.String())
		}
		var errorBody ErrorBody
		if err := json.Unmarshal(response.Body.Bytes(), &errorBody); err != nil {
			t.Fatal(err)
		}
		if errorBody.Error.Code != "invalid_request" {
			t.Fatalf("body %q: error = %#v", body, errorBody.Error)
		}
	}
}

func TestCommandErrorMappingUsesStableStatusCodeAndCommandID(t *testing.T) {
	tests := []struct {
		cause  error
		status int
		code   string
	}{
		{devices.ErrInvalidCommand, http.StatusBadRequest, "invalid_request"},
		{devices.ErrEntityNotFound, http.StatusNotFound, "entity_not_found"},
		{devices.ErrAdapterUnavailable, http.StatusServiceUnavailable, "adapter_unavailable"},
		{devices.ErrUpstreamRejected, http.StatusBadGateway, "upstream_rejected"},
		{devices.ErrOutcomeTimeout, http.StatusGatewayTimeout, "outcome_timeout"},
		{errors.New("SQLite unavailable"), http.StatusInternalServerError, "internal_error"},
	}
	for _, test := range tests {
		err := mapCommandError(&devices.CommandExecutionError{CommandID: apiCommandID, Err: test.cause})
		var status *statusError
		if !errors.As(err, &status) || status.GetStatus() != test.status || status.ErrorBody.Error.Code != test.code ||
			status.ErrorBody.Error.CommandID == nil || *status.ErrorBody.Error.CommandID != string(apiCommandID) {
			t.Fatalf("mapped %v = %#v", test.cause, status)
		}
	}
}
