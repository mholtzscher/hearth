package httpapi //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

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
	Register(huma.NewGroup(openapi, "/v1"), &stubDevices{}, nil)
	called = false
	status := huma.NewError(http.StatusTeapot, "sentinel message")
	if !called || status.GetStatus() != http.StatusTeapot {
		t.Fatalf("Register changed huma.NewError behavior: %#v", status)
	}
}
