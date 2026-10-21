package apicursor //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/pagination"
)

func TestCursorCodecsRoundTripAndEnforceScope(t *testing.T) {
	t.Parallel()
	deviceCursor, err := EncodeDevicesCursor(apiDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, err := DecodeDevicesCursor(deviceCursor)
	if err != nil || *deviceID != apiDeviceID {
		t.Fatalf("device cursor = %q, %v", deviceCursor, err)
	}

	deviceFilter := apiDeviceID
	entityCursor, err := EncodeEntitiesCursor(apiEntityID, &deviceFilter)
	if err != nil {
		t.Fatal(err)
	}
	entityID, err := DecodeEntitiesCursor(entityCursor, &deviceFilter)
	if err != nil || *entityID != apiEntityID {
		t.Fatalf("entity cursor = %q, %v", entityCursor, err)
	}
	if _, decodeErr := DecodeEntitiesCursor(entityCursor, nil); decodeErr == nil {
		t.Fatal("filtered entity cursor accepted without its filter")
	}

	deviceEntityCursor, err := EncodeDeviceEntitiesCursor(apiEntityID, apiDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	entityID, err = DecodeDeviceEntitiesCursor(deviceEntityCursor, apiDeviceID)
	if err != nil || *entityID != apiEntityID {
		t.Fatalf("device entity cursor = %q, %v", deviceEntityCursor, err)
	}
	if _, decodeErr := DecodeEntitiesCursor(deviceEntityCursor, &deviceFilter); decodeErr == nil {
		t.Fatal("device-detail cursor accepted by the Entity-list endpoint")
	}
	otherDevice := devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	if _, decodeErr := DecodeDeviceEntitiesCursor(deviceEntityCursor, otherDevice); decodeErr == nil {
		t.Fatal("device entity cursor accepted for another Device")
	}

	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 123, time.UTC)
	commandCursor, err := EncodeCommandCursor(
		devices.CommandRecord{ID: apiCommandID, EntityID: apiEntityID, RequestedAt: requestedAt},
	)
	if err != nil {
		t.Fatal(err)
	}
	decodedAt, commandID, err := DecodeCommandCursor(commandCursor, apiEntityID)
	if err != nil || *commandID != apiCommandID || !decodedAt.Equal(requestedAt) {
		t.Fatalf("command cursor = %q, %v", commandCursor, err)
	}
	otherEntity := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	if _, _, decodeErr := DecodeCommandCursor(commandCursor, otherEntity); decodeErr == nil {
		t.Fatal("command cursor accepted for another Entity")
	}

	healthCursor, err := encodeHealthCursor("adapter_health", apiAdapterID, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, decodeErr := DecodeEntityAvailabilityCursor(healthCursor, apiEntityID); decodeErr == nil {
		t.Fatal("Adapter health cursor accepted for Entity availability")
	}
}

func TestCommandListCursorRoundTripAndEnforcesScope(t *testing.T) {
	t.Parallel()
	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 123, time.UTC)
	record := devices.CommandRecord{ID: apiCommandID, EntityID: apiEntityID, RequestedAt: requestedAt}
	listCursor, err := EncodeCommandListCursor(record, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	decodedAt, commandID, err := DecodeCommandListCursor(listCursor, nil, nil)
	if err != nil || *commandID != apiCommandID || !decodedAt.Equal(requestedAt) {
		t.Fatalf("command list cursor = %q, %v", listCursor, err)
	}
	if _, _, decodeErr := DecodeCommandCursor(listCursor, apiEntityID); decodeErr == nil {
		t.Fatal("household command cursor accepted by the per-Entity endpoint")
	}
	perEntity, err := EncodeCommandCursor(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, decodeErr := DecodeCommandListCursor(perEntity, nil, nil); decodeErr == nil {
		t.Fatal("per-Entity command cursor accepted by the household endpoint")
	}
	listEntityID := apiEntityID
	filteredCursor, err := EncodeCommandListCursor(record, &listEntityID, new(devices.CommandStatusSatisfied))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, decodeErr := DecodeCommandListCursor(filteredCursor, nil, nil); decodeErr == nil {
		t.Fatal("filtered command cursor accepted without its filters")
	}
	if _, _, decodeErr := DecodeCommandListCursor(filteredCursor, &listEntityID, nil); decodeErr == nil {
		t.Fatal("filtered command cursor accepted with a dropped status filter")
	}
	status := devices.CommandStatusSatisfied
	if _, _, decodeErr := DecodeCommandListCursor(filteredCursor, &listEntityID, &status); decodeErr != nil {
		t.Fatalf("filtered command cursor rejected under its filters: %v", decodeErr)
	}
}

func TestCursorCodecsRejectMalformedDocuments(t *testing.T) {
	t.Parallel()
	unknownField := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"v":1,"resource":"devices","id":"` + string(apiDeviceID) + `","extra":true}`),
	)
	trailing := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"v":1,"resource":"devices","id":"` + string(apiDeviceID) + `"}{}`),
	)
	wrongVersion, _ := pagination.EncodeCursor(idCursor{Version: 2, Resource: "devices", ID: string(apiDeviceID)})
	wrongResource, _ := pagination.EncodeCursor(idCursor{Version: 1, Resource: "entities", ID: string(apiEntityID)})
	invalidID, _ := pagination.EncodeCursor(idCursor{Version: 1, Resource: "devices", ID: "dev_bad"})
	valid, _ := EncodeDevicesCursor(apiDeviceID)
	for _, value := range []string{"not base64!", valid + "=", unknownField, trailing, wrongVersion, wrongResource, invalidID} {
		if _, err := DecodeDevicesCursor(value); err == nil {
			t.Fatalf("DecodeDevicesCursor(%q) succeeded", value)
		}
	}

	nonUTC, _ := pagination.EncodeCursor(commandCursor{
		Version: 1, Resource: "entity_commands", EntityID: string(apiEntityID),
		RequestedAt: "2026-08-26T07:00:00-05:00", ID: string(apiCommandID),
	})
	if _, _, err := DecodeCommandCursor(nonUTC, apiEntityID); err == nil {
		t.Fatal("command cursor accepted a non-UTC timestamp")
	}
}

func TestEntityStateHistoryCursorRoundTripAndEnforcesScope(t *testing.T) {
	t.Parallel()
	otherEntity := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	cursor, err := EncodeEntityStateHistoryCursor(apiEntityID, devices.EntityStateHistoryFilterUpdates, 9)
	if err != nil {
		t.Fatal(err)
	}
	receiveOrder, err := DecodeEntityStateHistoryCursor(
		cursor,
		apiEntityID,
		devices.EntityStateHistoryFilterUpdates,
	)
	if err != nil || *receiveOrder != 9 {
		t.Fatalf("State history cursor = %q, %v", cursor, err)
	}
	if _, decodeErr := DecodeEntityStateHistoryCursor(
		cursor,
		apiEntityID,
		devices.EntityStateHistoryFilterAll,
	); decodeErr == nil {
		t.Fatal("State history cursor accepted for another filter")
	}
	if _, decodeErr := DecodeEntityStateHistoryCursor(
		cursor,
		otherEntity,
		devices.EntityStateHistoryFilterUpdates,
	); decodeErr == nil {
		t.Fatal("State history cursor accepted for another Entity")
	}
	if _, _, decodeErr := DecodeCommandCursor(cursor, apiEntityID); decodeErr == nil {
		t.Fatal("State history cursor accepted by the Command endpoint")
	}
}

func TestEntityStateHistoryCursorRejectsOutOfScopeDocuments(t *testing.T) {
	t.Parallel()
	valid, validErr := EncodeEntityStateHistoryCursor(apiEntityID, devices.EntityStateHistoryFilterUpdates, 9)
	if validErr != nil {
		t.Fatal(validErr)
	}
	unknownField := base64.RawURLEncoding.EncodeToString(
		[]byte(
			`{"v":1,"resource":"entity_state_history","parent_id":"` + string(
				apiEntityID,
			) + `","receive_order":9,"filter":"state-updates","extra":true}`,
		),
	)
	trailing := base64.RawURLEncoding.EncodeToString(
		[]byte(
			`{"v":1,"resource":"entity_state_history","parent_id":"` + string(
				apiEntityID,
			) + `","receive_order":9,"filter":"state-updates"}{}`,
		),
	)
	wrongVersion, _ := pagination.EncodeCursor(entityStateHistoryCursor{
		Version: 2, Resource: "entity_state_history", ParentID: string(apiEntityID),
		ReceiveOrder: 9, Filter: "state-updates",
	})
	wrongResource, _ := pagination.EncodeCursor(entityStateHistoryCursor{
		Version: 1, Resource: "entity_availability", ParentID: string(apiEntityID),
		ReceiveOrder: 9, Filter: "state-updates",
	})
	wrongFilter, _ := pagination.EncodeCursor(entityStateHistoryCursor{
		Version: 1, Resource: "entity_state_history", ParentID: string(apiEntityID),
		ReceiveOrder: 9, Filter: "recent",
	})
	zeroOrder, _ := pagination.EncodeCursor(entityStateHistoryCursor{
		Version: 1, Resource: "entity_state_history", ParentID: string(apiEntityID),
		ReceiveOrder: 0, Filter: "state-updates",
	})
	invalidParent, _ := pagination.EncodeCursor(entityStateHistoryCursor{
		Version: 1, Resource: "entity_state_history", ParentID: "bad",
		ReceiveOrder: 9, Filter: "state-updates",
	})
	for _, value := range []string{
		"not base64!", valid + "=", unknownField, trailing,
		wrongVersion, wrongResource, wrongFilter, zeroOrder, invalidParent,
	} {
		if _, decodeErr := DecodeEntityStateHistoryCursor(
			value,
			apiEntityID,
			devices.EntityStateHistoryFilterUpdates,
		); decodeErr == nil {
			t.Fatalf("DecodeEntityStateHistoryCursor(%q) succeeded", value)
		}
	}
}

const (
	apiAdapterID = "simulator"
	apiEntityID  = devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiDeviceID  = devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiCommandID = devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

func TestEntityEventCursorRoundTripAndEnforcesScope(t *testing.T) {
	t.Parallel()
	otherEntity := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	cursor, err := EncodeEntityEventCursor(apiEntityID, 9)
	if err != nil {
		t.Fatal(err)
	}
	receiveOrder, err := DecodeEntityEventCursor(cursor, apiEntityID)
	if err != nil || *receiveOrder != 9 {
		t.Fatalf("Entity Event cursor = %q, %v", cursor, err)
	}
	if _, decodeErr := DecodeEntityEventCursor(cursor, otherEntity); decodeErr == nil {
		t.Fatal("Entity Event cursor accepted for another Entity")
	}
	stateCursor, stateErr := EncodeEntityStateHistoryCursor(
		apiEntityID,
		devices.EntityStateHistoryFilterUpdates,
		9,
	)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if _, decodeErr := DecodeEntityEventCursor(stateCursor, apiEntityID); decodeErr == nil {
		t.Fatal("State history cursor accepted by the Entity Event endpoint")
	}
	if _, decodeErr := DecodeEntityStateHistoryCursor(
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
	wrongVersion, _ := pagination.EncodeCursor(entityEventCursor{
		Version: 2, Resource: "entity_events", ParentID: string(apiEntityID), ReceiveOrder: 9,
	})
	wrongResource, _ := pagination.EncodeCursor(entityEventCursor{
		Version: 1, Resource: "entity_state_history", ParentID: string(apiEntityID), ReceiveOrder: 9,
	})
	zeroOrder, _ := pagination.EncodeCursor(entityEventCursor{
		Version: 1, Resource: "entity_events", ParentID: string(apiEntityID), ReceiveOrder: 0,
	})
	invalidParent, _ := pagination.EncodeCursor(entityEventCursor{
		Version: 1, Resource: "entity_events", ParentID: "bad", ReceiveOrder: 9,
	})
	for _, value := range []string{
		"not base64!", cursor + "=", unknownField, trailing,
		wrongVersion, wrongResource, zeroOrder, invalidParent,
	} {
		if _, decodeErr := DecodeEntityEventCursor(value, apiEntityID); decodeErr == nil {
			t.Fatalf("DecodeEntityEventCursor(%q) succeeded", value)
		}
	}
}
