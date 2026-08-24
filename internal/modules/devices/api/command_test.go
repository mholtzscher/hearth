package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	apiCommandID     = devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiCorrelationID = devices.CorrelationID("cor_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

type apiCommandRepository struct {
	stubRepository
	mutex   sync.Mutex
	command devices.CommandRecord
}

func (repository *apiCommandRepository) CreateCommand(_ context.Context, command devices.CommandRecord) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.command = command
	return nil
}

func (repository *apiCommandRepository) MarkCommandAccepted(_ context.Context, _ devices.CommandID, acceptedAt time.Time) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.command.AcceptedAt = &acceptedAt
	if repository.command.Status == devices.CommandStatusRequested {
		repository.command.Status = devices.CommandStatusAccepted
	}
	return nil
}

func (repository *apiCommandRepository) CompleteCommand(_ context.Context, completion devices.CommandCompletion) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	completedAt := completion.CompletedAt
	failureCode := completion.FailureCode
	repository.command.Status = completion.Status
	repository.command.CompletedAt = &completedAt
	repository.command.FailureCode = &failureCode
	return nil
}

func (repository *apiCommandRepository) ProjectObservation(_ context.Context, params devices.ProjectObservationParams) (devices.ProjectionResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	completedAt := params.Now().UTC()
	observationID := params.Observation.ID
	repository.command.Status = devices.CommandStatusSatisfied
	repository.command.CompletedAt = &completedAt
	repository.command.OutcomeObservationID = &observationID
	return devices.ProjectionResult{
		Disposition: devices.DispositionUnchanged,
		SatisfiedCommand: &devices.CommandResult{
			CommandID: repository.command.ID, ObservationID: observationID,
			Value: append(devices.Value(nil), params.Observation.Value...),
		},
	}, nil
}

type apiSenderFunc func(context.Context, string, devices.CommandRequest) (devices.CommandAcceptance, error)

func (send apiSenderFunc) Send(ctx context.Context, adapterID string, request devices.CommandRequest) (devices.CommandAcceptance, error) {
	return send(ctx, adapterID, request)
}

func TestExecuteCommandReturnsSatisfiedResultAndRegistersOpenAPI(t *testing.T) {
	repository := &apiCommandRepository{stubRepository: stubRepository{view: apiEntityView(nil)}}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var service *devices.Service
	sender := apiSenderFunc(func(ctx context.Context, adapterID string, request devices.CommandRequest) (devices.CommandAcceptance, error) {
		observationID := apiObservationID
		_, err := service.ProjectObservation(ctx, adapterID, devices.Observation{
			ID: observationID, EntityID: request.EntityID, Value: devices.Value(`true`),
			AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &request.ID,
		}, time.Now().UTC())
		return devices.CommandAcceptance{Accepted: true}, err
	})
	service = devices.NewService(repository, sender, catalog, devices.Dependencies{
		NewCommandID:     func() (devices.CommandID, error) { return apiCommandID, nil },
		NewCorrelationID: func() (devices.CorrelationID, error) { return apiCorrelationID, nil },
	})
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	Register(huma.NewGroup(openapi, "/v1/entities"), service)

	request := httptest.NewRequest(http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/commands", bytes.NewBufferString(`{"operation":"set","parameters":{"value":true}}`))
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
	if body.CommandID != string(apiCommandID) || body.Status != "satisfied" || body.ObservationID != string(apiObservationID) || body.Value != true {
		t.Fatalf("body = %#v", body)
	}
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}/commands"].Post
	if operation == nil || operation.OperationID != "execute-entity-command" {
		t.Fatalf("POST operation = %#v", operation)
	}
}

func TestExecuteCommandMapsMalformedBodiesToStableBadRequest(t *testing.T) {
	router, _ := testAPI(t, &stubRepository{view: apiEntityView(nil)})
	for _, body := range []string{`{`, `{"operation":"set"}`} {
		request := httptest.NewRequest(http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/commands", bytes.NewBufferString(body))
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
