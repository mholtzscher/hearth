package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func listableCommand() devices.CommandRecord {
	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 123, time.UTC)
	return devices.CommandRecord{
		ID: apiCommandID, EntityID: apiEntityID, AdapterID: "simulator",
		OperationName: "set", Parameters: devices.CommandParameters(`{"value":true}`),
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab", Status: devices.CommandStatusSatisfied,
		RequestedAt: requestedAt, DeadlineAt: requestedAt.Add(10 * time.Second),
	}
}

func TestListCommandsReturnsNewestFirstPageAndRegistersOpenAPI(t *testing.T) {
	t.Parallel()
	var requested devices.ListCommandsParams
	stub := &stubDevices{listCommands: func(
		_ context.Context,
		params devices.ListCommandsParams,
	) (devices.Page[devices.CommandRecord], error) {
		requested = params
		return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{
			listableCommand(),
		}}, nil
	}}
	router, openapi := testAPI(t, stub)

	response := performRequest(router, "/v1/commands?limit=50")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body CommandCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != string(apiCommandID) ||
		body.Items[0].Status != "satisfied" || body.NextCursor != nil {
		t.Fatalf("body = %#v", body)
	}
	if requested.Limit != 50 || requested.EntityID != nil || requested.Status != nil ||
		requested.BeforeRequestedAt != nil || requested.BeforeID != nil {
		t.Fatalf("ListCommands params = %#v", requested)
	}
	operation := openapi.OpenAPI().Paths["/v1/commands"].Get
	if operation == nil || operation.OperationID != "list-commands" ||
		operation.Summary != "List household Command history" || len(operation.Tags) != 1 ||
		operation.Tags[0] != "Commands" {
		t.Fatalf("GET operation = %#v", operation)
	}
}

func TestListCommandsForwardsFiltersAndCursor(t *testing.T) {
	t.Parallel()
	status := devices.CommandStatusSatisfied
	entityID := apiEntityID
	first := listableCommand()
	cursor, err := encodeCommandListCursor(first, &entityID, &status)
	if err != nil {
		t.Fatal(err)
	}
	var requested devices.ListCommandsParams
	stub := &stubDevices{listCommands: func(
		_ context.Context,
		params devices.ListCommandsParams,
	) (devices.Page[devices.CommandRecord], error) {
		requested = params
		last := listableCommand()
		return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{last}, HasMore: true}, nil
	}}
	router, _ := testAPI(t, stub)

	response := performRequest(
		router,
		"/v1/commands?limit=10&entity_id="+string(apiEntityID)+"&status=satisfied&cursor="+cursor,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if requested.EntityID == nil || *requested.EntityID != apiEntityID ||
		requested.Status == nil || *requested.Status != devices.CommandStatusSatisfied ||
		requested.BeforeRequestedAt == nil || requested.BeforeID == nil ||
		*requested.BeforeID != apiCommandID {
		t.Fatalf("ListCommands params = %#v", requested)
	}
	var body CommandCollectionBody
	if unmarshalErr := json.Unmarshal(response.Body.Bytes(), &body); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if body.NextCursor == nil {
		t.Fatal("HasMore page omitted next_cursor")
	}
	_, _, cursorErr := decodeCommandListCursor(*body.NextCursor, requested.EntityID, requested.Status)
	if cursorErr != nil {
		t.Fatalf("next cursor does not decode under request filters: %v", cursorErr)
	}
}

func TestListCommandsMapsStandardErrors(t *testing.T) {
	t.Parallel()
	first := listableCommand()
	unfilteredCursor, err := encodeCommandListCursor(first, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	entityCursor, err := encodeCommandCursor(first)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		path    string
		devices *stubDevices
		status  int
		detail  string
	}{
		{
			"invalid entity filter",
			"/v1/commands?entity_id=not-an-id",
			&stubDevices{},
			http.StatusBadRequest,
			"entity_id must be a canonical Hearth Entity ID",
		},
		{
			"invalid status filter",
			"/v1/commands?status=exploded",
			&stubDevices{},
			http.StatusBadRequest,
			"invalid status",
		},
		{
			"per-entity cursor rejected",
			"/v1/commands?cursor=" + entityCursor,
			&stubDevices{},
			http.StatusBadRequest,
			"invalid cursor",
		},
		{
			"cursor filter mismatch",
			"/v1/commands?entity_id=" + string(apiEntityID) + "&cursor=" + unfilteredCursor,
			&stubDevices{},
			http.StatusBadRequest,
			"invalid cursor",
		},
		{
			"invalid page",
			"/v1/commands",
			&stubDevices{listCommands: func(
				context.Context,
				devices.ListCommandsParams,
			) (devices.Page[devices.CommandRecord], error) {
				return devices.Page[devices.CommandRecord]{}, devices.ErrInvalidPage
			}},
			http.StatusBadRequest,
			"invalid page",
		},
		{
			"internal",
			"/v1/commands",
			&stubDevices{listCommands: func(
				context.Context,
				devices.ListCommandsParams,
			) (devices.Page[devices.CommandRecord], error) {
				return devices.Page[devices.CommandRecord]{}, errors.New("SQLite unavailable")
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
			var errorBody struct {
				Status int    `json:"status"`
				Detail string `json:"detail"`
			}
			if unmarshalErr := json.Unmarshal(response.Body.Bytes(), &errorBody); unmarshalErr != nil {
				t.Fatal(unmarshalErr)
			}
			if errorBody.Status != test.status || errorBody.Detail != test.detail {
				t.Fatalf("error body = %#v", errorBody)
			}
		})
	}
}
