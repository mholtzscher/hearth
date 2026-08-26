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
	cursor := idCursor{Version: cursorVersion, Resource: "entities", ID: string(id)}
	if deviceID != nil {
		cursor.DeviceID = string(*deviceID)
	}
	return encodeCursor(cursor)
}

func decodeEntitiesCursor(value string, deviceID *devices.DeviceID) (*devices.EntityID, error) {
	var cursor idCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	requestedDeviceID := ""
	if deviceID != nil {
		requestedDeviceID = string(*deviceID)
	}
	if cursor.Version != cursorVersion || cursor.Resource != "entities" || cursor.DeviceID != requestedDeviceID {
		return nil, errors.New("invalid entity cursor scope")
	}
	id, err := devices.ParseEntityID(cursor.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid entity cursor ID: %w", err)
	}
	return &id, nil
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
	if err := decoder.Decode(cursor); err != nil {
		return fmt.Errorf("decode cursor document: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("cursor contains trailing JSON")
	}
	return nil
}
