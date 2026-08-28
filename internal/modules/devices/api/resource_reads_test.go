package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestListDevicesDefaultsLimitAndReturnsScopedCursor(t *testing.T) {
	t.Parallel()
	secondDeviceID := devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	calls := 0
	stub := &stubDevices{
		listDevices: func(_ context.Context, params devices.ListDevicesParams) (devices.Page[devices.Device], error) {
			calls++
			if params.Limit != 50 {
				t.Fatalf("limit = %d", params.Limit)
			}
			if calls == 1 {
				if params.AfterID != nil {
					t.Fatalf("first AfterID = %v", params.AfterID)
				}
				return devices.Page[devices.Device]{
					Items:   []devices.Device{{ID: apiDeviceID, Kind: devices.DeviceKindLight, Name: "Office"}},
					HasMore: true,
				}, nil
			}
			if params.AfterID == nil || *params.AfterID != apiDeviceID {
				t.Fatalf("second AfterID = %v", params.AfterID)
			}
			return devices.Page[devices.Device]{
				Items: []devices.Device{{ID: secondDeviceID, Kind: devices.DeviceKindLight, Name: "Kitchen"}},
			}, nil
		},
	}
	router, openapi := testAPI(t, stub)
	response := performRequest(router, "/v1/devices")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var first DeviceCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	response = performRequest(router, "/v1/devices?cursor="+*first.NextCursor)
	if response.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", response.Code, response.Body.String())
	}
	var second DeviceCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("second page = %#v", second)
	}
	operation := openapi.OpenAPI().Paths["/v1/devices"].Get
	if operation == nil || operation.OperationID != "list-devices" || operation.Summary != "List Devices" {
		t.Fatalf("list devices operation = %#v", operation)
	}
}

//nolint:gocognit // The related response-shape assertions are intentionally kept together.
func TestDeviceDetailAndEntityListUseFullEntityBodies(t *testing.T) {
	t.Parallel()
	view := apiEntityWithState(nil)
	secondEntityID := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	secondView := apiEntityWithState(nil)
	secondView.Entity.ID = secondEntityID
	deviceCalls := 0
	stub := &stubDevices{
		getDevice: func(_ context.Context, params devices.GetDeviceParams) (devices.DeviceAggregate, error) {
			deviceCalls++
			if params.ID != apiDeviceID || params.EntityLimit != 50 {
				t.Fatalf("device params = %#v", params)
			}
			page := devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}, HasMore: true}
			if deviceCalls == 2 {
				if params.AfterEntityID == nil || *params.AfterEntityID != apiEntityID {
					t.Fatalf("second device params = %#v", params)
				}
				page = devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{secondView}}
			}
			return devices.DeviceAggregate{
				Device:   devices.Device{ID: apiDeviceID, Kind: devices.DeviceKindLight, Name: "Office"},
				Entities: page,
			}, nil
		},
		listEntities: func(_ context.Context, params devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
			if params.DeviceID == nil || *params.DeviceID != apiDeviceID || params.Limit != 2 {
				t.Fatalf("entity params = %#v", params)
			}
			return devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}}, nil
		},
	}
	router, _ := testAPI(t, stub)
	response := performRequest(router, "/v1/devices/"+string(apiDeviceID))
	if response.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", response.Code, response.Body.String())
	}
	var detail DeviceDetailBody
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Entities) != 1 || detail.Entities[0].ID != string(apiEntityID) || detail.Entities[0].State != nil ||
		detail.NextEntityCursor == nil {
		t.Fatalf("device detail = %#v", detail)
	}
	response = performRequest(router, "/v1/devices/"+string(apiDeviceID)+"?entity_cursor="+*detail.NextEntityCursor)
	if response.Code != http.StatusOK {
		t.Fatalf("second detail status = %d, body = %s", response.Code, response.Body.String())
	}
	detail = DeviceDetailBody{}
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Entities) != 1 || detail.Entities[0].ID != string(secondEntityID) || detail.NextEntityCursor != nil {
		t.Fatalf("second device detail = %#v", detail)
	}
	response = performRequest(router, "/v1/entities?device_id="+string(apiDeviceID)+"&limit=2")
	if response.Code != http.StatusOK {
		t.Fatalf("entities status = %d, body = %s", response.Code, response.Body.String())
	}
	var entitiesBody EntityCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &entitiesBody); err != nil {
		t.Fatal(err)
	}
	if len(entitiesBody.Items) != 1 || entitiesBody.Items[0].Support["operations"] == nil {
		t.Fatalf("entities body = %#v", entitiesBody)
	}
}

func TestCommandReadBodiesOmitInternalIdentifiers(t *testing.T) {
	t.Parallel()
	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 123, time.UTC)
	acceptedAt := requestedAt.Add(time.Second)
	command := devices.CommandRecord{
		ID: apiCommandID, EntityID: apiEntityID, AdapterID: "simulator", OperationName: devices.OperationNameSet,
		Parameters: devices.CommandParameters(`{"value":true}`), CorrelationID: "cor_internal",
		Status: devices.CommandStatusAccepted, RequestedAt: requestedAt, DeadlineAt: requestedAt.Add(10 * time.Second),
		AcceptedAt: &acceptedAt,
	}
	stub := &stubDevices{
		getCommand: func(_ context.Context, id devices.CommandID) (devices.CommandRecord, error) {
			if id != apiCommandID {
				t.Fatalf("command ID = %q", id)
			}
			return command, nil
		},
		listEntityCommands: func(_ context.Context, params devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error) {
			if params.EntityID != apiEntityID || params.Limit != 50 {
				t.Fatalf("history params = %#v", params)
			}
			return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{command}}, nil
		},
	}
	router, _ := testAPI(t, stub)
	for _, path := range []string{
		"/v1/commands/" + string(apiCommandID),
		"/v1/entities/" + string(apiEntityID) + "/commands",
	} {
		response := performRequest(router, path)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, response.Code, response.Body.String())
		}
		var raw any
		if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(raw)
		if containsJSONField(encoded, "adapter_id") || containsJSONField(encoded, "correlation_id") {
			t.Fatalf("%s leaked an internal identifier: %s", path, encoded)
		}
	}
}

func containsJSONField(encoded []byte, field string) bool {
	var value any
	_ = json.Unmarshal(encoded, &value)
	return findJSONField(value, field)
}

func findJSONField(value any, field string) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, ok := value[field]; ok {
			return true
		}
		for _, child := range value {
			if findJSONField(child, field) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if findJSONField(child, field) {
				return true
			}
		}
	}
	return false
}
