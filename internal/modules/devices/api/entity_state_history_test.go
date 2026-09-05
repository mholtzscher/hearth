package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func apiStateHistoryEntry(receiveOrder int64) devices.EntityStateHistoryEntry {
	adapterReceivedAt := time.Date(2026, 8, 25, 10, 0, 0, 123456789, time.UTC)
	observedAt := adapterReceivedAt.Add(time.Second)
	sourceUpdatedAt := adapterReceivedAt.Add(-time.Minute)
	return devices.EntityStateHistoryEntry{
		ObservationID:     apiObservationID,
		Value:             devices.Value(`true`),
		Disposition:       devices.DispositionApplied,
		AdapterReceivedAt: adapterReceivedAt,
		SourceUpdatedAt:   &sourceUpdatedAt,
		ObservedAt:        observedAt,
		ReceiveOrder:      receiveOrder,
	}
}

func assertMappedStateHistoryEntry(t *testing.T, entry EntityStateHistoryBody) {
	t.Helper()
	if entry.ObservationID != string(apiObservationID) || entry.Value != true {
		t.Fatalf("history entry = %#v", entry)
	}
	if entry.Disposition != "applied" || entry.RejectionCode != nil {
		t.Fatalf("history entry = %#v", entry)
	}
	if entry.AdapterReceivedAt != "2026-08-25T10:00:00.123456789Z" {
		t.Fatalf("history entry = %#v", entry)
	}
	if entry.SourceUpdatedAt == nil || *entry.SourceUpdatedAt != "2026-08-25T09:59:00.123456789Z" {
		t.Fatalf("history entry = %#v", entry)
	}
	if entry.ObservedAt != "2026-08-25T10:00:01.123456789Z" {
		t.Fatalf("history entry = %#v", entry)
	}
}

// This test protects State history entry mapping and fails if transport
// timestamps, values, or rejection codes drift from the domain contract.
func TestListEntityStateHistoryReturnsMappedEntries(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{listEntityStateHistory: func(
		_ context.Context,
		params devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error) {
		if params.EntityID != apiEntityID || params.Limit != 50 {
			t.Fatalf("history params = %#v", params)
		}
		if params.Filter != devices.EntityStateHistoryFilterUpdates || params.BeforeReceiveOrder != nil {
			t.Fatalf("first history params = %#v", params)
		}
		return devices.Page[devices.EntityStateHistoryEntry]{
			Items:   []devices.EntityStateHistoryEntry{apiStateHistoryEntry(9)},
			HasMore: true,
		}, nil
	}}
	router, openapi := testAPI(t, stub)

	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/state/history")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body EntityStateHistoryCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.NextCursor == nil {
		t.Fatalf("page = %#v", body)
	}
	entry := body.Items[0]
	assertMappedStateHistoryEntry(t, entry)
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}/state/history"].Get
	if operation == nil || operation.OperationID != "list-entity-state-history" {
		t.Fatalf("State history operation = %#v", operation)
	}
	if operation.Summary != "List an Entity's State history" || len(operation.Tags) != 1 ||
		operation.Tags[0] != "Entities" {
		t.Fatalf("State history operation = %#v", operation)
	}
	if _, ok := operation.Responses["422"]; !ok {
		t.Fatalf("State history operation is missing standard 422 response: %#v", operation.Responses)
	}
}

// This test protects State history cursor pagination and fails if the
// continuation cursor loses its receive-order position.
func TestListEntityStateHistoryPaginatesWithScopedCursor(t *testing.T) {
	t.Parallel()
	calls := 0
	stub := &stubDevices{listEntityStateHistory: func(
		_ context.Context,
		params devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error) {
		calls++
		if calls == 1 {
			if params.BeforeReceiveOrder != nil {
				t.Fatalf("first history params = %#v", params)
			}
			return devices.Page[devices.EntityStateHistoryEntry]{
				Items:   []devices.EntityStateHistoryEntry{apiStateHistoryEntry(9)},
				HasMore: true,
			}, nil
		}
		if params.BeforeReceiveOrder == nil || *params.BeforeReceiveOrder != 9 {
			t.Fatalf("second history params = %#v", params)
		}
		return devices.Page[devices.EntityStateHistoryEntry]{
			Items: []devices.EntityStateHistoryEntry{apiStateHistoryEntry(4)},
		}, nil
	}}
	router, _ := testAPI(t, stub)

	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/state/history")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var first EntityStateHistoryCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	response = performRequest(router, "/v1/entities/"+string(apiEntityID)+"/state/history?cursor="+*first.NextCursor)
	if response.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", response.Code, response.Body.String())
	}
	var second EntityStateHistoryCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("second page = %#v", second)
	}
	if calls != 2 {
		t.Fatalf("service calls = %d", calls)
	}
}

// This test protects rejected-row omission and fails if a rejected row leaks
// a value or a malformed accepted value is exposed instead of a 500.
func TestListEntityStateHistoryOmitsRejectedValues(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 25, 10, 0, 1, 0, time.UTC)
	rejection := devices.RejectionInvalidValue
	rejected := devices.EntityStateHistoryEntry{
		ObservationID:     apiObservationID,
		Disposition:       devices.DispositionRejected,
		Rejection:         &rejection,
		AdapterReceivedAt: observedAt,
		ObservedAt:        observedAt,
		ReceiveOrder:      7,
	}
	stub := &stubDevices{listEntityStateHistory: func(
		context.Context,
		devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error) {
		return devices.Page[devices.EntityStateHistoryEntry]{Items: []devices.EntityStateHistoryEntry{rejected}}, nil
	}}
	router, _ := testAPI(t, stub)
	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/state/history?disposition=rejected")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if containsJSONField(response.Body.Bytes(), "value") {
		t.Fatalf("rejected row exposed a value: %s", response.Body.String())
	}
	var body EntityStateHistoryCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Disposition != "rejected" ||
		body.Items[0].RejectionCode == nil || *body.Items[0].RejectionCode != "invalid_value" ||
		body.Items[0].SourceUpdatedAt != nil {
		t.Fatalf("rejected body = %#v", body)
	}

	broken := apiStateHistoryEntry(6)
	broken.Value = devices.Value(`{invalid`)
	brokenRouter, _ := testAPI(t, &stubDevices{listEntityStateHistory: func(
		context.Context,
		devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error) {
		return devices.Page[devices.EntityStateHistoryEntry]{Items: []devices.EntityStateHistoryEntry{broken}}, nil
	}})
	brokenResponse := performRequest(brokenRouter, "/v1/entities/"+string(apiEntityID)+"/state/history")
	if brokenResponse.Code != http.StatusInternalServerError {
		t.Fatalf("malformed value status = %d, body = %s", brokenResponse.Code, brokenResponse.Body.String())
	}
}

// This test protects the State history error contract and fails if an
// unsupported filter returns 422 or a cursor crosses Entities or filters.
func TestListEntityStateHistoryMapsErrors(t *testing.T) {
	t.Parallel()
	cursor, cursorErr := encodeEntityStateHistoryCursor(apiEntityID, devices.EntityStateHistoryFilterUpdates, 9)
	if cursorErr != nil {
		t.Fatal(cursorErr)
	}
	for _, test := range []struct {
		name    string
		path    string
		devices *stubDevices
		status  int
		detail  string
	}{
		{
			"invalid ID",
			"/v1/entities/not-an-id/state/history",
			&stubDevices{},
			http.StatusBadRequest,
			"entity_id must be a canonical Hearth Entity ID",
		},
		{
			"invalid cursor",
			"/v1/entities/" + string(apiEntityID) + "/state/history?cursor=not-base64!",
			&stubDevices{},
			http.StatusBadRequest,
			"invalid cursor",
		},
		{
			"cursor from another filter",
			"/v1/entities/" + string(apiEntityID) + "/state/history?disposition=all&cursor=" + cursor,
			&stubDevices{},
			http.StatusBadRequest,
			"invalid cursor",
		},
		{
			"unsupported filter",
			"/v1/entities/" + string(apiEntityID) + "/state/history?disposition=recent",
			&stubDevices{listEntityStateHistory: func(
				context.Context,
				devices.ListEntityStateHistoryParams,
			) (devices.Page[devices.EntityStateHistoryEntry], error) {
				return devices.Page[devices.EntityStateHistoryEntry]{}, devices.ErrInvalidPage
			}},
			http.StatusBadRequest,
			"invalid page",
		},
		{
			"not found",
			"/v1/entities/" + string(apiEntityID) + "/state/history",
			&stubDevices{listEntityStateHistory: func(
				context.Context,
				devices.ListEntityStateHistoryParams,
			) (devices.Page[devices.EntityStateHistoryEntry], error) {
				return devices.Page[devices.EntityStateHistoryEntry]{}, devices.ErrEntityNotFound
			}},
			http.StatusNotFound,
			"entity not found",
		},
		{
			"internal",
			"/v1/entities/" + string(apiEntityID) + "/state/history",
			&stubDevices{listEntityStateHistory: func(
				context.Context,
				devices.ListEntityStateHistoryParams,
			) (devices.Page[devices.EntityStateHistoryEntry], error) {
				return devices.Page[devices.EntityStateHistoryEntry]{}, errors.New("SQLite unavailable")
			}},
			http.StatusInternalServerError,
			"internal error",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(t, test.devices)
			response := performRequest(router, test.path)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var problem huma.ErrorModel
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Status != test.status || problem.Detail != test.detail {
				t.Fatalf("error body = %#v", problem)
			}
		})
	}
}

// This test protects Huma structural validation and fails if out-of-range
// limits return 400 instead of 422.
func TestListEntityStateHistoryRejectsOutOfRangeLimits(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	for _, path := range []string{
		"/v1/entities/" + string(apiEntityID) + "/state/history?limit=0",
		"/v1/entities/" + string(apiEntityID) + "/state/history?limit=201",
	} {
		response := performRequest(router, path)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
}

// This test protects empty-history encoding and fails if an Entity without
// retained history returns null items instead of an empty array.
func TestListEntityStateHistoryEncodesEmptyItemsArray(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{listEntityStateHistory: func(
		context.Context,
		devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error) {
		return devices.Page[devices.EntityStateHistoryEntry]{}, nil
	}})
	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/state/history")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	items, ok := raw["items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("items = %v in %s", raw["items"], response.Body.String())
	}
	if _, hasCursor := raw["next_cursor"]; hasCursor {
		t.Fatalf("empty page exposed next_cursor: %s", response.Body.String())
	}
}
