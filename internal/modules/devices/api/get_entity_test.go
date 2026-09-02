package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

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
	getEntity                func(context.Context, devices.EntityID) (devices.EntityWithState, error)
	setEntityEnabled         func(context.Context, devices.EntityID, bool) (devices.EntityWithState, error)
	listDevices              func(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error)
	getDevice                func(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error)
	listEntities             func(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error)
	getCommand               func(context.Context, devices.CommandID) (devices.CommandRecord, error)
	listEntityCommands       func(context.Context, devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error)
	listAdapters             func(context.Context, devices.ListAdaptersParams) (devices.Page[devices.AdapterInstance], error)
	getAdapter               func(context.Context, string) (devices.AdapterInstance, error)
	listAdapterHealthHistory func(
		context.Context,
		devices.ListAdapterHealthParams,
	) (devices.Page[devices.HealthTransition], error)
	listEntityAvailabilityHistory func(
		context.Context,
		devices.ListEntityAvailabilityParams,
	) (devices.Page[devices.HealthTransition], error)
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

func (stub *stubDevices) SetEntityEnabled(
	ctx context.Context,
	entityID devices.EntityID,
	enabled bool,
) (devices.EntityWithState, error) {
	if stub.setEntityEnabled == nil {
		panic("unexpected SetEntityEnabled call")
	}
	return stub.setEntityEnabled(ctx, entityID, enabled)
}

func (stub *stubDevices) ListDevices(
	ctx context.Context,
	params devices.ListDevicesParams,
) (devices.Page[devices.Device], error) {
	if stub.listDevices == nil {
		panic("unexpected ListDevices call")
	}
	return stub.listDevices(ctx, params)
}

func (stub *stubDevices) GetDevice(
	ctx context.Context,
	params devices.GetDeviceParams,
) (devices.DeviceAggregate, error) {
	if stub.getDevice == nil {
		panic("unexpected GetDevice call")
	}
	return stub.getDevice(ctx, params)
}

func (stub *stubDevices) ListEntities(
	ctx context.Context,
	params devices.ListEntitiesParams,
) (devices.Page[devices.EntityWithState], error) {
	if stub.listEntities == nil {
		panic("unexpected ListEntities call")
	}
	return stub.listEntities(ctx, params)
}

func (stub *stubDevices) GetCommand(ctx context.Context, id devices.CommandID) (devices.CommandRecord, error) {
	if stub.getCommand == nil {
		panic("unexpected GetCommand call")
	}
	return stub.getCommand(ctx, id)
}

func (stub *stubDevices) ListEntityCommands(
	ctx context.Context,
	params devices.ListEntityCommandsParams,
) (devices.Page[devices.CommandRecord], error) {
	if stub.listEntityCommands == nil {
		panic("unexpected ListEntityCommands call")
	}
	return stub.listEntityCommands(ctx, params)
}

func (stub *stubDevices) ListAdapters(
	ctx context.Context,
	params devices.ListAdaptersParams,
) (devices.Page[devices.AdapterInstance], error) {
	if stub.listAdapters == nil {
		panic("unexpected ListAdapters call")
	}
	return stub.listAdapters(ctx, params)
}

func (stub *stubDevices) GetAdapter(ctx context.Context, adapterID string) (devices.AdapterInstance, error) {
	if stub.getAdapter == nil {
		panic("unexpected GetAdapter call")
	}
	return stub.getAdapter(ctx, adapterID)
}

func (stub *stubDevices) ListAdapterHealthHistory(
	ctx context.Context,
	params devices.ListAdapterHealthParams,
) (devices.Page[devices.HealthTransition], error) {
	if stub.listAdapterHealthHistory == nil {
		panic("unexpected ListAdapterHealthHistory call")
	}
	return stub.listAdapterHealthHistory(ctx, params)
}

func (stub *stubDevices) ListEntityAvailabilityHistory(
	ctx context.Context,
	params devices.ListEntityAvailabilityParams,
) (devices.Page[devices.HealthTransition], error) {
	if stub.listEntityAvailabilityHistory == nil {
		panic("unexpected ListEntityAvailabilityHistory call")
	}
	return stub.listEntityAvailabilityHistory(ctx, params)
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
	t.Parallel()
	var requestedEntityID devices.EntityID
	stub := &stubDevices{
		getEntity: func(_ context.Context, entityID devices.EntityID) (devices.EntityWithState, error) {
			requestedEntityID = entityID
			return apiEntityWithState(nil), nil
		},
	}
	router, openapi := testAPI(t, stub)

	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body EntityBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if requestedEntityID != apiEntityID {
		t.Fatalf("GetEntity ID = %q", requestedEntityID)
	}
	if body.ID != string(apiEntityID) || body.AdapterID != "simulator" || body.Name != "Power" ||
		!body.Enabled || body.State != nil || body.Support["state"] == nil ||
		body.Availability.Status != "available" || body.Availability.Source != "entity_report" ||
		body.Availability.SourceObservedAt == nil {
		t.Fatalf("body = %#v", body)
	}
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}"].Get
	if operation == nil || operation.OperationID != "get-entity" {
		t.Fatalf("GET operation = %#v", operation)
	}
}

func TestGetEntityMapsCurrentState(t *testing.T) {
	t.Parallel()
	adapterReceivedAt := time.Date(2026, 8, 22, 12, 0, 0, 123, time.UTC)
	sourceUpdatedAt := adapterReceivedAt.Add(-time.Minute)
	observedAt := adapterReceivedAt.Add(time.Second)
	state := &devices.State{
		EntityID:          apiEntityID,
		Value:             devices.Value(`true`),
		ObservationID:     apiObservationID,
		AdapterReceivedAt: adapterReceivedAt,
		SourceUpdatedAt:   &sourceUpdatedAt,
		ObservedAt:        observedAt,
		ReceiveOrder:      4,
	}
	router, _ := testAPI(
		t,
		&stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
			return apiEntityWithState(state), nil
		}},
	)
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

func TestGetEntityMapsStandardErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		devices *stubDevices
		status  int
		detail  string
	}{
		{
			"invalid ID",
			"/v1/entities/not-an-id",
			&stubDevices{},
			http.StatusBadRequest,
			"entity_id must be a canonical Hearth Entity ID",
		},
		{
			"not found",
			"/v1/entities/" + string(apiEntityID),
			&stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
				return devices.EntityWithState{}, devices.ErrEntityNotFound
			}},
			http.StatusNotFound,
			"entity not found",
		},
		{
			"internal",
			"/v1/entities/" + string(apiEntityID),
			&stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
				return devices.EntityWithState{}, errors.New("SQLite unavailable")
			}},
			http.StatusInternalServerError,
			"internal error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(t, test.devices)
			response := performRequest(router, test.path)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var body huma.ErrorModel
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Status != test.status || body.Detail != test.detail {
				t.Fatalf("error body = %#v", body)
			}
		})
	}
}

func apiEntityWithState(state *devices.State) devices.EntityWithState {
	evidenceAt := time.Date(2026, 8, 22, 12, 0, 1, 0, time.UTC)
	sourceObservedAt := evidenceAt.Add(-time.Second)
	return devices.EntityWithState{
		Entity: devices.Entity{
			ID:        apiEntityID,
			DeviceID:  apiDeviceID,
			AdapterID: "simulator",
			Name:      "Power",
			TypeID:    devices.EntityTypePowerV1,
			Support:   devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			Enabled:   true,
		},
		State: state,
		Availability: devices.EntityAvailability{
			Status: devices.EntityAvailabilityAvailable, Source: "entity_report",
			Since: evidenceAt, EvidenceAt: evidenceAt, SourceObservedAt: &sourceObservedAt,
		},
	}
}

func testAPI(t *testing.T, devices Devices) (*echo.Echo, huma.API) {
	t.Helper()
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	group := huma.NewGroup(openapi, "/v1")
	Register(group, devices)
	return router, openapi
}

//nolint:paralleltest,reassign // Temporarily replaces the process-wide Huma error factory.
func TestRegisterDoesNotChangeHumaErrorFactory(t *testing.T) {
	original := huma.NewError
	called := false
	huma.NewError = func(status int, message string, details ...error) huma.StatusError {
		called = true
		return original(status, message, details...)
	}
	t.Cleanup(func() { huma.NewError = original })

	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	Register(huma.NewGroup(openapi, "/v1"), &stubDevices{})
	called = false
	status := huma.NewError(http.StatusTeapot, "sentinel message")
	if !called || status.GetStatus() != http.StatusTeapot {
		t.Fatalf("Register changed huma.NewError behavior: %#v", status)
	}
}

func performRequest(router http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
