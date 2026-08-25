package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	apiEntityID      = devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiDeviceID      = devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiObservationID = devices.ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

type stubEntityReader struct {
	view devices.EntityView
	err  error
}

func (reader *stubEntityReader) GetEntity(context.Context, devices.EntityID) (devices.EntityView, error) {
	return reader.view, reader.err
}

type stubCommandExecutor struct {
	result devices.CommandResult
	err    error
}

func (executor *stubCommandExecutor) ExecuteCommand(
	context.Context,
	devices.EntityID,
	devices.OperationName,
	devices.CommandParameters,
) (devices.CommandResult, error) {
	return executor.result, executor.err
}

func TestGetEntityReturnsMetadataAndNullableState(t *testing.T) {
	router, openapi := testAPI(t, &stubEntityReader{view: apiEntityView(nil)}, &stubCommandExecutor{})
	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		ID      string         `json:"id"`
		Name    string         `json:"name"`
		Support map[string]any `json:"support"`
		State   any            `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != string(apiEntityID) || body.Name != "Power" || body.State != nil || body.Support["state"] == nil {
		t.Fatalf("body = %#v", body)
	}
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}"].Get
	if operation == nil || operation.OperationID != "get-entity" {
		t.Fatalf("GET operation = %#v", operation)
	}
}

func TestGetEntityMapsCurrentState(t *testing.T) {
	adapterReceivedAt := time.Date(2026, 8, 22, 12, 0, 0, 123, time.UTC)
	sourceUpdatedAt := adapterReceivedAt.Add(-time.Minute)
	state := &devices.State{
		EntityID: apiEntityID, Value: devices.Value(`true`), ObservationID: apiObservationID,
		AdapterReceivedAt: adapterReceivedAt, SourceUpdatedAt: &sourceUpdatedAt,
		ObservedAt: adapterReceivedAt.Add(time.Second), ReceiveOrder: 4,
	}
	router, _ := testAPI(t, &stubEntityReader{view: apiEntityView(state)}, &stubCommandExecutor{})
	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body EntityBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.State == nil || body.State.Value != true || body.State.ObservationID != string(apiObservationID) ||
		body.State.SourceUpdatedAt == nil || *body.State.SourceUpdatedAt != sourceUpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("body = %#v", body)
	}
}

func TestGetEntityMapsStableErrors(t *testing.T) {
	tests := []struct {
		path   string
		reader *stubEntityReader
		status int
		code   string
	}{
		{"/v1/entities/not-an-id", &stubEntityReader{}, http.StatusBadRequest, "invalid_request"},
		{"/v1/entities/" + string(apiEntityID), &stubEntityReader{err: devices.ErrEntityNotFound}, http.StatusNotFound, "entity_not_found"},
		{"/v1/entities/" + string(apiEntityID), &stubEntityReader{err: errors.New("SQLite unavailable")}, http.StatusInternalServerError, "internal_error"},
	}
	for _, test := range tests {
		router, _ := testAPI(t, test.reader, &stubCommandExecutor{})
		response := performRequest(router, test.path)
		if response.Code != test.status {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		var body ErrorBody
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != test.code {
			t.Fatalf("error body = %#v", body)
		}
	}
}

func apiEntityView(state *devices.State) devices.EntityView {
	return devices.EntityView{
		Entity: devices.Entity{
			ID: apiEntityID, DeviceID: apiDeviceID, AdapterID: "simulator", Name: "Power",
			TypeID: devices.EntityTypePowerV1, Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		},
		State: state,
	}
}

func testAPI(t *testing.T, entities EntityReader, commands CommandExecutor) (*echo.Echo, huma.API) {
	t.Helper()
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	Register(huma.NewGroup(openapi, "/v1/entities"), Dependencies{Entities: entities, Commands: commands})
	return router, openapi
}

func performRequest(router http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
