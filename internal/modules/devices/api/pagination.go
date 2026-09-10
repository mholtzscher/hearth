package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const cursorVersion = 1

type idCursor struct {
	Version  int    `json:"v"`
	Resource string `json:"resource"`
	ID       string `json:"id"`
	DeviceID string `json:"device_id,omitempty"`
}

type commandCursor struct {
	Version     int    `json:"v"`
	Resource    string `json:"resource"`
	EntityID    string `json:"entity_id"`
	RequestedAt string `json:"requested_at"`
	ID          string `json:"id"`
}

type healthCursor struct {
	Version      int    `json:"v"`
	Resource     string `json:"resource"`
	ParentID     string `json:"parent_id"`
	ReceiveOrder int64  `json:"receive_order"`
}

// entityStateHistoryCursor positions an Entity State history page after the
// last returned receive order within one Entity and filter.
type entityStateHistoryCursor struct {
	Version      int    `json:"v"`
	Resource     string `json:"resource"`
	ParentID     string `json:"parent_id"`
	ReceiveOrder int64  `json:"receive_order"`
	Filter       string `json:"filter"`
}

// entityEventCursor positions an Entity Event history page after
// the last returned receive order within one Entity. Event history has no
// filter field, and the resource name keeps the cursor mutually incompatible
// with State history cursors even though both carry a receive order.
type entityEventCursor struct {
	Version      int    `json:"v"`
	Resource     string `json:"resource"`
	ParentID     string `json:"parent_id"`
	ReceiveOrder int64  `json:"receive_order"`
}

func encodeDevicesCursor(id devices.DeviceID) (string, error) {
	return encodeCursor(idCursor{Version: cursorVersion, Resource: "devices", ID: string(id)})
}

func decodeDevicesCursor(value string) (*devices.DeviceID, error) {
	var cursor idCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "devices" || cursor.DeviceID != "" {
		return nil, errors.New("invalid device cursor scope")
	}
	id, err := devices.ParseDeviceID(cursor.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid device cursor ID: %w", err)
	}
	return &id, nil
}

func encodeEntitiesCursor(id devices.EntityID, deviceID *devices.DeviceID) (string, error) {
	requestedDeviceID := ""
	if deviceID != nil {
		requestedDeviceID = string(*deviceID)
	}
	return encodeEntityCursor("entities", id, requestedDeviceID)
}

func decodeEntitiesCursor(value string, deviceID *devices.DeviceID) (*devices.EntityID, error) {
	requestedDeviceID := ""
	if deviceID != nil {
		requestedDeviceID = string(*deviceID)
	}
	return decodeEntityCursor(value, "entities", requestedDeviceID)
}

func encodeDeviceEntitiesCursor(id devices.EntityID, deviceID devices.DeviceID) (string, error) {
	return encodeEntityCursor("device_entities", id, string(deviceID))
}

func decodeDeviceEntitiesCursor(value string, deviceID devices.DeviceID) (*devices.EntityID, error) {
	return decodeEntityCursor(value, "device_entities", string(deviceID))
}

func encodeEntityCursor(resource string, id devices.EntityID, deviceID string) (string, error) {
	return encodeCursor(idCursor{
		Version: cursorVersion, Resource: resource, ID: string(id), DeviceID: deviceID,
	})
}

func decodeEntityCursor(value, resource, deviceID string) (*devices.EntityID, error) {
	var cursor idCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != resource || cursor.DeviceID != deviceID {
		return nil, errors.New("invalid entity cursor scope")
	}
	id, err := devices.ParseEntityID(cursor.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid entity cursor ID: %w", err)
	}
	return &id, nil
}

func encodeAdaptersCursor(id string) (string, error) {
	return encodeCursor(idCursor{Version: cursorVersion, Resource: "adapters", ID: id})
}

func decodeAdaptersCursor(value string) (*string, error) {
	var cursor idCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "adapters" ||
		cursor.DeviceID != "" || !validAdapterID(cursor.ID) {
		return nil, errors.New("invalid Adapter cursor scope")
	}
	return &cursor.ID, nil
}

func encodeAdapterHealthCursor(adapterID string, receiveOrder int64) (string, error) {
	return encodeHealthCursor("adapter_health", adapterID, receiveOrder)
}

func decodeAdapterHealthCursor(value, adapterID string) (*int64, error) {
	return decodeHealthCursor(value, "adapter_health", adapterID)
}

func encodeEntityAvailabilityCursor(entityID devices.EntityID, receiveOrder int64) (string, error) {
	return encodeHealthCursor("entity_availability", string(entityID), receiveOrder)
}

func decodeEntityAvailabilityCursor(value string, entityID devices.EntityID) (*int64, error) {
	return decodeHealthCursor(value, "entity_availability", string(entityID))
}

func encodeEntityStateHistoryCursor(
	entityID devices.EntityID,
	filter devices.EntityStateHistoryFilter,
	receiveOrder int64,
) (string, error) {
	return encodeCursor(entityStateHistoryCursor{
		Version: cursorVersion, Resource: "entity_state_history", ParentID: string(entityID),
		ReceiveOrder: receiveOrder, Filter: string(filter),
	})
}

func decodeEntityStateHistoryCursor(
	value string,
	entityID devices.EntityID,
	filter devices.EntityStateHistoryFilter,
) (*int64, error) {
	var cursor entityStateHistoryCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "entity_state_history" ||
		cursor.ParentID != string(entityID) || cursor.Filter != string(filter) ||
		cursor.ReceiveOrder < 1 {
		return nil, errors.New("invalid State history cursor scope")
	}
	if _, err := devices.ParseEntityID(cursor.ParentID); err != nil {
		return nil, fmt.Errorf("invalid State history cursor entity ID: %w", err)
	}
	switch devices.EntityStateHistoryFilter(cursor.Filter) {
	case devices.EntityStateHistoryFilterUpdates,
		devices.EntityStateHistoryFilterAll,
		devices.EntityStateHistoryFilterApplied,
		devices.EntityStateHistoryFilterUnchanged,
		devices.EntityStateHistoryFilterRejected:
	default:
		return nil, errors.New("invalid State history cursor scope")
	}
	return &cursor.ReceiveOrder, nil
}

func encodeEntityEventCursor(entityID devices.EntityID, receiveOrder int64) (string, error) {
	return encodeCursor(entityEventCursor{
		Version: cursorVersion, Resource: "entity_events", ParentID: string(entityID),
		ReceiveOrder: receiveOrder,
	})
}

func decodeEntityEventCursor(value string, entityID devices.EntityID) (*int64, error) {
	var cursor entityEventCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "entity_events" ||
		cursor.ParentID != string(entityID) || cursor.ReceiveOrder < 1 {
		return nil, errors.New("invalid Entity Event history cursor scope")
	}
	if _, err := devices.ParseEntityID(cursor.ParentID); err != nil {
		return nil, fmt.Errorf("invalid Entity Event history cursor entity ID: %w", err)
	}
	return &cursor.ReceiveOrder, nil
}

func encodeHealthCursor(resource, parentID string, receiveOrder int64) (string, error) {
	return encodeCursor(healthCursor{
		Version: cursorVersion, Resource: resource, ParentID: parentID, ReceiveOrder: receiveOrder,
	})
}

func decodeHealthCursor(value, resource, parentID string) (*int64, error) {
	var cursor healthCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != resource || cursor.ParentID != parentID ||
		cursor.ReceiveOrder < 1 {
		return nil, errors.New("invalid health cursor scope")
	}
	return &cursor.ReceiveOrder, nil
}

func encodeCommandCursor(command devices.CommandRecord) (string, error) {
	return encodeCursor(commandCursor{
		Version: cursorVersion, Resource: "entity_commands", EntityID: string(command.EntityID),
		RequestedAt: formatTime(command.RequestedAt), ID: string(command.ID),
	})
}

func decodeCommandCursor(value string, entityID devices.EntityID) (*time.Time, *devices.CommandID, error) {
	var cursor commandCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "entity_commands" || cursor.EntityID != string(entityID) {
		return nil, nil, errors.New("invalid command cursor scope")
	}
	if _, err := devices.ParseEntityID(cursor.EntityID); err != nil {
		return nil, nil, fmt.Errorf("invalid command cursor entity ID: %w", err)
	}
	id, err := devices.ParseCommandID(cursor.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid command cursor ID: %w", err)
	}
	requestedAt, err := time.Parse(time.RFC3339Nano, cursor.RequestedAt)
	if err != nil || requestedAt.IsZero() || cursor.RequestedAt != requestedAt.UTC().Format(time.RFC3339Nano) {
		return nil, nil, errors.New("invalid command cursor requested_at")
	}
	return &requestedAt, &id, nil
}

func encodeCursor(cursor any) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(value string, cursor any) error {
	if value == "" {
		return errors.New("cursor is empty")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("decode cursor: %w", err)
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != value {
		return errors.New("cursor encoding is not canonical")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(cursor); decodeErr != nil {
		return fmt.Errorf("decode cursor document: %w", decodeErr)
	}
	if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
		return errors.New("cursor contains trailing JSON")
	}
	return nil
}
