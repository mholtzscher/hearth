package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gocognit // Each resource cursor is checked against its endpoint-specific scope.
func TestCursorCodecsRoundTripAndEnforceScope(t *testing.T) {
	t.Parallel()
	deviceCursor, err := encodeDevicesCursor(apiDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, err := decodeDevicesCursor(deviceCursor)
	if err != nil || *deviceID != apiDeviceID {
		t.Fatalf("device cursor = %q, %v", deviceCursor, err)
	}

	deviceFilter := apiDeviceID
	entityCursor, err := encodeEntitiesCursor(apiEntityID, &deviceFilter)
	if err != nil {
		t.Fatal(err)
	}
	entityID, err := decodeEntitiesCursor(entityCursor, &deviceFilter)
	if err != nil || *entityID != apiEntityID {
		t.Fatalf("entity cursor = %q, %v", entityCursor, err)
	}
	if _, decodeErr := decodeEntitiesCursor(entityCursor, nil); decodeErr == nil {
		t.Fatal("filtered entity cursor accepted without its filter")
	}

	deviceEntityCursor, err := encodeDeviceEntitiesCursor(apiEntityID, apiDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	entityID, err = decodeDeviceEntitiesCursor(deviceEntityCursor, apiDeviceID)
	if err != nil || *entityID != apiEntityID {
		t.Fatalf("device entity cursor = %q, %v", deviceEntityCursor, err)
	}
	if _, decodeErr := decodeEntitiesCursor(deviceEntityCursor, &deviceFilter); decodeErr == nil {
		t.Fatal("device-detail cursor accepted by the Entity-list endpoint")
	}
	otherDevice := devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	if _, decodeErr := decodeDeviceEntitiesCursor(deviceEntityCursor, otherDevice); decodeErr == nil {
		t.Fatal("device entity cursor accepted for another Device")
	}

	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 123, time.UTC)
	commandCursor, err := encodeCommandCursor(
		devices.CommandRecord{ID: apiCommandID, EntityID: apiEntityID, RequestedAt: requestedAt},
	)
	if err != nil {
		t.Fatal(err)
	}
	decodedAt, commandID, err := decodeCommandCursor(commandCursor, apiEntityID)
	if err != nil || *commandID != apiCommandID || !decodedAt.Equal(requestedAt) {
		t.Fatalf("command cursor = %q, %v", commandCursor, err)
	}
	otherEntity := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	if _, _, decodeErr := decodeCommandCursor(commandCursor, otherEntity); decodeErr == nil {
		t.Fatal("command cursor accepted for another Entity")
	}

	adapterCursor, err := encodeAdaptersCursor(apiAdapterID)
	if err != nil {
		t.Fatal(err)
	}
	adapterID, err := decodeAdaptersCursor(adapterCursor)
	if err != nil || *adapterID != apiAdapterID {
		t.Fatalf("Adapter cursor = %q, %v", adapterCursor, err)
	}

	healthCursor, err := encodeAdapterHealthCursor(apiAdapterID, 42)
	if err != nil {
		t.Fatal(err)
	}
	receiveOrder, err := decodeAdapterHealthCursor(healthCursor, apiAdapterID)
	if err != nil || *receiveOrder != 42 {
		t.Fatalf("health cursor = %q, %v", healthCursor, err)
	}
	if _, decodeErr := decodeAdapterHealthCursor(healthCursor, "other"); decodeErr == nil {
		t.Fatal("health cursor accepted for another Adapter")
	}
	if _, decodeErr := decodeEntityAvailabilityCursor(healthCursor, apiEntityID); decodeErr == nil {
		t.Fatal("Adapter health cursor accepted for Entity availability")
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
	wrongVersion, _ := encodeCursor(idCursor{Version: 2, Resource: "devices", ID: string(apiDeviceID)})
	wrongResource, _ := encodeCursor(idCursor{Version: 1, Resource: "entities", ID: string(apiEntityID)})
	invalidID, _ := encodeCursor(idCursor{Version: 1, Resource: "devices", ID: "dev_bad"})
	valid, _ := encodeDevicesCursor(apiDeviceID)
	for _, value := range []string{"not base64!", valid + "=", unknownField, trailing, wrongVersion, wrongResource, invalidID} {
		if _, err := decodeDevicesCursor(value); err == nil {
			t.Fatalf("decodeDevicesCursor(%q) succeeded", value)
		}
	}

	nonUTC, _ := encodeCursor(commandCursor{
		Version: 1, Resource: "entity_commands", EntityID: string(apiEntityID),
		RequestedAt: "2026-08-26T07:00:00-05:00", ID: string(apiCommandID),
	})
	if _, _, err := decodeCommandCursor(nonUTC, apiEntityID); err == nil {
		t.Fatal("command cursor accepted a non-UTC timestamp")
	}
}

func TestEntityStateHistoryCursorRoundTripAndEnforcesScope(t *testing.T) {
	t.Parallel()
	otherEntity := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	cursor, err := encodeEntityStateHistoryCursor(apiEntityID, devices.EntityStateHistoryFilterUpdates, 9)
	if err != nil {
		t.Fatal(err)
	}
	receiveOrder, err := decodeEntityStateHistoryCursor(
		cursor,
		apiEntityID,
		devices.EntityStateHistoryFilterUpdates,
	)
	if err != nil || *receiveOrder != 9 {
		t.Fatalf("State history cursor = %q, %v", cursor, err)
	}
	if _, decodeErr := decodeEntityStateHistoryCursor(
		cursor,
		apiEntityID,
		devices.EntityStateHistoryFilterAll,
	); decodeErr == nil {
		t.Fatal("State history cursor accepted for another filter")
	}
	if _, decodeErr := decodeEntityStateHistoryCursor(
		cursor,
		otherEntity,
		devices.EntityStateHistoryFilterUpdates,
	); decodeErr == nil {
		t.Fatal("State history cursor accepted for another Entity")
	}
	if _, _, decodeErr := decodeCommandCursor(cursor, apiEntityID); decodeErr == nil {
		t.Fatal("State history cursor accepted by the Command endpoint")
	}
}

func TestEntityStateHistoryCursorRejectsOutOfScopeDocuments(t *testing.T) {
	t.Parallel()
	valid, validErr := encodeEntityStateHistoryCursor(apiEntityID, devices.EntityStateHistoryFilterUpdates, 9)
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
	wrongVersion, _ := encodeCursor(entityStateHistoryCursor{
		Version: 2, Resource: "entity_state_history", ParentID: string(apiEntityID),
		ReceiveOrder: 9, Filter: "state-updates",
	})
	wrongResource, _ := encodeCursor(entityStateHistoryCursor{
		Version: 1, Resource: "entity_availability", ParentID: string(apiEntityID),
		ReceiveOrder: 9, Filter: "state-updates",
	})
	wrongFilter, _ := encodeCursor(entityStateHistoryCursor{
		Version: 1, Resource: "entity_state_history", ParentID: string(apiEntityID),
		ReceiveOrder: 9, Filter: "recent",
	})
	zeroOrder, _ := encodeCursor(entityStateHistoryCursor{
		Version: 1, Resource: "entity_state_history", ParentID: string(apiEntityID),
		ReceiveOrder: 0, Filter: "state-updates",
	})
	invalidParent, _ := encodeCursor(entityStateHistoryCursor{
		Version: 1, Resource: "entity_state_history", ParentID: "bad",
		ReceiveOrder: 9, Filter: "state-updates",
	})
	for _, value := range []string{
		"not base64!", valid + "=", unknownField, trailing,
		wrongVersion, wrongResource, wrongFilter, zeroOrder, invalidParent,
	} {
		if _, decodeErr := decodeEntityStateHistoryCursor(
			value,
			apiEntityID,
			devices.EntityStateHistoryFilterUpdates,
		); decodeErr == nil {
			t.Fatalf("decodeEntityStateHistoryCursor(%q) succeeded", value)
		}
	}
}
