package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const apiEntityEventID = devices.EntityEventID("evt_01890f47-7a6b-7c4d-8e9f-0123456789ab")

func apiEntityEventEntry(receiveOrder int64) devices.EntityEventHistoryEntry {
	emittedAt := time.Date(2026, 8, 25, 10, 0, 0, 123456789, time.UTC)
	return devices.EntityEventHistoryEntry{
		EventID:      apiEntityEventID,
		EntityID:     apiEntityID,
		Name:         devices.EntityEventName("single_press"),
		Disposition:  devices.EntityEventDispositionAccepted,
		EmittedAt:    emittedAt,
		ReceivedAt:   emittedAt.Add(time.Second),
		RecordedAt:   emittedAt.Add(2 * time.Second),
		ReceiveOrder: receiveOrder,
	}
}

func assertMappedEntityEventEntry(t *testing.T, entry EntityEventBody) {
	t.Helper()
	if entry.EventID != string(apiEntityEventID) || entry.EntityID != string(apiEntityID) ||
		entry.Name != "single_press" || entry.Disposition != "accepted" || entry.RejectionCode != nil {
		t.Fatalf("Entity Event entry = %#v", entry)
	}
	if entry.EmittedAt != "2026-08-25T10:00:00.123456789Z" ||
		entry.ReceivedAt != "2026-08-25T10:00:01.123456789Z" ||
		entry.RecordedAt != "2026-08-25T10:00:02.123456789Z" {
		t.Fatalf("Entity Event timestamps = %#v", entry)
	}
}

// This test protects Entity Event history entry mapping and fails if transport
// timestamps or dispositions drift from the domain contract.
func TestListEntityEventsReturnsMappedEntries(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{listEntityEvents: func(
		_ context.Context,
		params devices.ListEntityEventsParams,
	) (devices.Page[devices.EntityEventHistoryEntry], error) {
		if params.EntityID != apiEntityID || params.Limit != 50 || params.BeforeReceiveOrder != nil {
			t.Fatalf("Entity Event params = %#v", params)
		}
		return devices.Page[devices.EntityEventHistoryEntry]{
			Items:   []devices.EntityEventHistoryEntry{apiEntityEventEntry(9)},
			HasMore: true,
		}, nil
	}}
	router, openapi := testAPI(t, stub)

	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/events")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body EntityEventCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.NextCursor == nil {
		t.Fatalf("page = %#v", body)
	}
	assertMappedEntityEventEntry(t, body.Items[0])
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}/events"].Get
	if operation == nil || operation.OperationID != "list-entity-events" {
		t.Fatalf("Entity Event operation = %#v", operation)
	}
	if operation.Summary != "List an Entity's Entity Event history" || len(operation.Tags) != 1 ||
		operation.Tags[0] != "Entities" {
		t.Fatalf("Entity Event operation = %#v", operation)
	}
	if _, ok := operation.Responses["422"]; !ok {
		t.Fatalf("Entity Event operation is missing standard 422 response: %#v", operation.Responses)
	}
}

// This test protects Entity Event cursor pagination and fails if the
// continuation cursor loses its exclusive receive-order position.
func TestListEntityEventsPaginatesWithScopedCursor(t *testing.T) {
	t.Parallel()
	calls := 0
	stub := &stubDevices{listEntityEvents: func(
		_ context.Context,
		params devices.ListEntityEventsParams,
	) (devices.Page[devices.EntityEventHistoryEntry], error) {
		calls++
		if calls == 1 {
			if params.BeforeReceiveOrder != nil {
				t.Fatalf("first Entity Event params = %#v", params)
			}
			return devices.Page[devices.EntityEventHistoryEntry]{
				Items:   []devices.EntityEventHistoryEntry{apiEntityEventEntry(9)},
				HasMore: true,
			}, nil
		}
		if params.BeforeReceiveOrder == nil || *params.BeforeReceiveOrder != 9 {
			t.Fatalf("second Entity Event params = %#v", params)
		}
		return devices.Page[devices.EntityEventHistoryEntry]{
			Items: []devices.EntityEventHistoryEntry{apiEntityEventEntry(4)},
		}, nil
	}}
	router, _ := testAPI(t, stub)

	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/events")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var first EntityEventCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	response = performRequest(router, "/v1/entities/"+string(apiEntityID)+"/events?cursor="+*first.NextCursor)
	if response.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", response.Code, response.Body.String())
	}
	var second EntityEventCollectionBody
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

// This test protects the Entity Event history error contract and fails if a
// parent, cursor, or page error maps to the wrong status.
func TestListEntityEventsMapsErrors(t *testing.T) {
	t.Parallel()
	stateCursor, cursorErr := encodeEntityStateHistoryCursor(
		apiEntityID,
		devices.EntityStateHistoryFilterUpdates,
		9,
	)
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
			"/v1/entities/not-an-id/events",
			&stubDevices{},
			http.StatusBadRequest,
			"entity_id must be a canonical Hearth Entity ID",
		},
		{
			"invalid cursor",
			"/v1/entities/" + string(apiEntityID) + "/events?cursor=not-base64!",
			&stubDevices{},
			http.StatusBadRequest,
			"invalid cursor",
		},
		{
			"State history cursor is a different scope",
			"/v1/entities/" + string(apiEntityID) + "/events?cursor=" + stateCursor,
			&stubDevices{},
			http.StatusBadRequest,
			"invalid cursor",
		},
		{
			"invalid page",
			"/v1/entities/" + string(apiEntityID) + "/events",
			&stubDevices{listEntityEvents: func(
				context.Context,
				devices.ListEntityEventsParams,
			) (devices.Page[devices.EntityEventHistoryEntry], error) {
				return devices.Page[devices.EntityEventHistoryEntry]{}, devices.ErrInvalidPage
			}},
			http.StatusBadRequest,
			"invalid page",
		},
		{
			"unknown parent",
			"/v1/entities/" + string(apiEntityID) + "/events",
			&stubDevices{listEntityEvents: func(
				context.Context,
				devices.ListEntityEventsParams,
			) (devices.Page[devices.EntityEventHistoryEntry], error) {
				return devices.Page[devices.EntityEventHistoryEntry]{}, devices.ErrEntityNotFound
			}},
			http.StatusNotFound,
			"entity not found",
		},
		{
			"internal",
			"/v1/entities/" + string(apiEntityID) + "/events",
			&stubDevices{listEntityEvents: func(
				context.Context,
				devices.ListEntityEventsParams,
			) (devices.Page[devices.EntityEventHistoryEntry], error) {
				return devices.Page[devices.EntityEventHistoryEntry]{}, errors.New("SQLite unavailable")
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
func TestListEntityEventsRejectsOutOfRangeLimits(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	for _, path := range []string{
		"/v1/entities/" + string(apiEntityID) + "/events?limit=0",
		"/v1/entities/" + string(apiEntityID) + "/events?limit=201",
	} {
		response := performRequest(router, path)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
}

// This test protects empty-history encoding and fails if an Entity with no
// events returns null items instead of an empty array.
func TestListEntityEventsEncodesEmptyItemsArray(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{listEntityEvents: func(
		context.Context,
		devices.ListEntityEventsParams,
	) (devices.Page[devices.EntityEventHistoryEntry], error) {
		return devices.Page[devices.EntityEventHistoryEntry]{}, nil
	}})
	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/events")
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

// This test protects the private field boundary and fails if Adapter, runtime,
// or correlation IDs, the fingerprint, receive order, or a raw envelope leak
// into a public Entity Event body. A rejection code stays visible.
func TestListEntityEventsOmitsPrivateFields(t *testing.T) {
	t.Parallel()
	rejection := devices.EntityEventRejectionUnsupportedEvent
	rejected := apiEntityEventEntry(9)
	rejected.Disposition = devices.EntityEventDispositionRejected
	rejected.Rejection = &rejection
	accepted := apiEntityEventEntry(8)
	stub := &stubDevices{listEntityEvents: func(
		context.Context,
		devices.ListEntityEventsParams,
	) (devices.Page[devices.EntityEventHistoryEntry], error) {
		return devices.Page[devices.EntityEventHistoryEntry]{
			Items: []devices.EntityEventHistoryEntry{rejected, accepted},
		}, nil
	}}
	router, _ := testAPI(t, stub)
	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/events")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, field := range []string{
		"adapter_id", "runtime_id", "correlation_id", "fingerprint", "receive_order",
		"adapter", "runtime", "envelope", "causation_id", "support",
	} {
		if containsJSONField(response.Body.Bytes(), field) {
			t.Fatalf("%s leaked into %s", field, response.Body.String())
		}
	}
	var body EntityEventCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0].RejectionCode == nil ||
		*body.Items[0].RejectionCode != "unsupported_event" || body.Items[1].RejectionCode != nil {
		t.Fatalf("Entity Event bodies = %#v", body.Items)
	}
}

// This test protects the Entity Event cursor codec and fails if a cursor loses
// its Entity, resource, or exclusive position scope.
func TestEntityEventCursorRoundTripAndEnforcesScope(t *testing.T) {
	t.Parallel()
	otherEntity := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	cursor, err := encodeEntityEventCursor(apiEntityID, 9)
	if err != nil {
		t.Fatal(err)
	}
	receiveOrder, err := decodeEntityEventCursor(cursor, apiEntityID)
	if err != nil || *receiveOrder != 9 {
		t.Fatalf("Entity Event cursor = %q, %v", cursor, err)
	}
	if _, decodeErr := decodeEntityEventCursor(cursor, otherEntity); decodeErr == nil {
		t.Fatal("Entity Event cursor accepted for another Entity")
	}
	stateCursor, stateErr := encodeEntityStateHistoryCursor(
		apiEntityID,
		devices.EntityStateHistoryFilterUpdates,
		9,
	)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if _, decodeErr := decodeEntityEventCursor(stateCursor, apiEntityID); decodeErr == nil {
		t.Fatal("State history cursor accepted by the Entity Event endpoint")
	}
	if _, decodeErr := decodeEntityStateHistoryCursor(
		cursor,
		apiEntityID,
		devices.EntityStateHistoryFilterUpdates,
	); decodeErr == nil {
		t.Fatal("Entity Event cursor accepted by the State history endpoint")
	}

	unknownField := base64.RawURLEncoding.EncodeToString(
		[]byte(
			`{"v":1,"resource":"entity_events","parent_id":"` + string(
				apiEntityID,
			) + `","receive_order":9,"filter":"all"}`,
		),
	)
	trailing := base64.RawURLEncoding.EncodeToString(
		[]byte(
			`{"v":1,"resource":"entity_events","parent_id":"` + string(
				apiEntityID,
			) + `","receive_order":9}{}`,
		),
	)
	wrongVersion, _ := encodeCursor(entityEventCursor{
		Version: 2, Resource: "entity_events", ParentID: string(apiEntityID), ReceiveOrder: 9,
	})
	wrongResource, _ := encodeCursor(entityEventCursor{
		Version: 1, Resource: "entity_state_history", ParentID: string(apiEntityID), ReceiveOrder: 9,
	})
	zeroOrder, _ := encodeCursor(entityEventCursor{
		Version: 1, Resource: "entity_events", ParentID: string(apiEntityID), ReceiveOrder: 0,
	})
	invalidParent, _ := encodeCursor(entityEventCursor{
		Version: 1, Resource: "entity_events", ParentID: "bad", ReceiveOrder: 9,
	})
	for _, value := range []string{
		"not base64!", cursor + "=", unknownField, trailing,
		wrongVersion, wrongResource, zeroOrder, invalidParent,
	} {
		if _, decodeErr := decodeEntityEventCursor(value, apiEntityID); decodeErr == nil {
			t.Fatalf("decodeEntityEventCursor(%q) succeeded", value)
		}
	}
}
