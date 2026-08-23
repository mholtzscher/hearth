package hearthd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

type testRepository struct {
	view devices.EntityView
}

func (*testRepository) RegisterBinding(context.Context, devices.RegisterBindingParams) (devices.Binding, error) {
	panic("unexpected RegisterBinding call")
}

func (repository *testRepository) GetEntityView(context.Context, devices.EntityID) (devices.EntityView, error) {
	return repository.view, nil
}

func (*testRepository) ProjectObservation(context.Context, devices.ProjectObservationParams) (devices.ProjectionResult, error) {
	panic("unexpected ProjectObservation call")
}

func (*testRepository) DeleteExpiredObservationReceipts(context.Context, time.Time) error {
	panic("unexpected DeleteExpiredObservationReceipts call")
}

func TestHTTPHandlerServesHealthReadinessAndDeviceOperations(t *testing.T) {
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := &testRepository{view: devices.EntityView{Entity: devices.Entity{
		ID: testHTTPEntityID, DeviceID: testHTTPDeviceID, AdapterID: "simulator", Name: "Power",
		TypeID: devices.EntityTypePowerV1, Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
	}}}
	service := devices.NewService(repository, catalog, devices.Dependencies{})
	readiness := &testReadiness{}
	handler, api := NewHTTPHandler(service, readiness)

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
}

func appRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
