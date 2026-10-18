package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const cursorVersion = 1

type idCursor struct {
	Version  int    `json:"v"`
	Resource string `json:"resource"`
	ID       string `json:"id"`
	DeviceID string `json:"device_id,omitempty"`
}

type healthCursor struct {
	Version      int    `json:"v"`
	Resource     string `json:"resource"`
	ParentID     string `json:"parent_id"`
	ReceiveOrder int64  `json:"receive_order"`
}

func encodeAdaptersCursor(id string) (string, error) {
	return encodeCursor(idCursor{Version: cursorVersion, Resource: "adapters", ID: id})
}

func decodeAdaptersCursor(value string) (*string, error) {
	var cursor idCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "adapters" || cursor.DeviceID != "" ||
		!validAdapterID(cursor.ID) {
		return nil, errors.New("invalid Adapter cursor scope")
	}
	return &cursor.ID, nil
}

func encodeAdapterHealthCursor(adapterID string, receiveOrder int64) (string, error) {
	return encodeCursor(
		healthCursor{
			Version:      cursorVersion,
			Resource:     "adapter_health",
			ParentID:     adapterID,
			ReceiveOrder: receiveOrder,
		},
	)
}

func decodeAdapterHealthCursor(value, adapterID string) (*int64, error) {
	var cursor healthCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "adapter_health" || cursor.ParentID != adapterID ||
		cursor.ReceiveOrder < 1 {
		return nil, errors.New("invalid health cursor scope")
	}
	return &cursor.ReceiveOrder, nil
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
