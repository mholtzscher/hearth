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

func TestGetEntityReturnsMetadataAndNullableState(t *testing.T) {
	var requestedEntityID devices.EntityID
	stub := &stubDevices{getEntity: func(_ context.Context, entityID devices.EntityID) (devices.EntityView, error) {
		requestedEntityID = entityID
		return apiEntityView(nil), nil
	}}
	router, openapi := testAPI(t, stub)

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
	if requestedEntityID != apiEntityID {
		t.Fatalf("GetEntity ID = %q", requestedEntityID)
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
	observedAt := adapterReceivedAt.Add(time.Second)
	state := &devices.State{
		EntityID: apiEntityID, Value: devices.Value(`true`), ObservationID: apiObservationID,
		AdapterReceivedAt: adapterReceivedAt, SourceUpdatedAt: &sourceUpdatedAt, ObservedAt: observedAt, ReceiveOrder: 4,
	}
	router, _ := testAPI(t, &stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityView, error) {
		return apiEntityView(state), nil
	}})
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
		name    string
		path    string
		devices *stubDevices
		status  int
		code    string
	}{
		{"invalid ID", "/v1/entities/not-an-id", &stubDevices{}, http.StatusBadRequest, "invalid_request"},
		{"not found", "/v1/entities/" + string(apiEntityID), &stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityView, error) {
			return devices.EntityView{}, devices.ErrEntityNotFound
		}}, http.StatusNotFound, "entity_not_found"},
		{"internal", "/v1/entities/" + string(apiEntityID), &stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityView, error) {
			return devices.EntityView{}, errors.New("SQLite unavailable")
		}}, http.StatusInternalServerError, "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, _ := testAPI(t, test.devices)
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
		})
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

func testAPI(t *testing.T, devices Devices) (*echo.Echo, huma.API) {
	t.Helper()
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	group := huma.NewGroup(openapi, "/v1/entities")
	Register(group, devices)
	return router, openapi
}

func TestRegisterDoesNotChangeHumaErrorFactory(t *testing.T) {
	original := huma.NewError
	called := false
	huma.NewError = func(status int, message string, _ ...error) huma.StatusError {
		called = true
		return NewStatusError(status, "sentinel", message)
	}
	t.Cleanup(func() { huma.NewError = original })

	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	Register(huma.NewGroup(openapi, "/v1/entities"), &stubDevices{})
	called = false
	status := huma.NewError(http.StatusTeapot, "sentinel message")
	if !called {
		t.Fatal("Register replaced huma.NewError")
	}
	var sentinel *statusError
	if !errors.As(status, &sentinel) || sentinel.ErrorBody.Error.Code != "sentinel" {
		t.Fatalf("huma.NewError result = %#v", status)
	}
}

func performRequest(router http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
