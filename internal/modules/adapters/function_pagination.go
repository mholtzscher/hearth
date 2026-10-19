package adapters

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"time"
)

type functionCursor struct {
	Version   int          `json:"version"`
	Kind      string       `json:"kind"`
	AdapterID string       `json:"adapter_id"`
	Revision  CatalogID    `json:"revision,omitempty"`
	Key       FunctionKey  `json:"key,omitempty"`
	Time      string       `json:"time,omitempty"`
	ID        InvocationID `json:"id,omitempty"`
}

func ValidateFunctionPage(page PageInput) error {
	const maximumCursorSize = 2048
	if (page.Limit != 0 && !ValidPageLimit(page.Limit)) || len(page.Cursor) > maximumCursorSize {
		return ErrInvalidFunctionInput
	}
	return nil
}

func ValidateInvocationFilter(filter InvocationFilter) error {
	if ValidateAdapterID(filter.AdapterID) != nil ||
		(filter.FunctionKey != "" && !ValidFunctionKey(filter.FunctionKey)) ||
		ValidateFunctionPage(filter.PageInput) != nil {
		return ErrInvalidFunctionInput
	}
	return nil
}

func FunctionPageLimit(input int) (int, error) {
	const defaultLimit = 50
	if input == 0 {
		return defaultLimit, nil
	}
	if !ValidPageLimit(input) {
		return 0, ErrInvalidFunctionInput
	}
	return input, nil
}

func EncodeFunctionCursor(adapterID string, revision CatalogID, key FunctionKey) string {
	return encodeFunctionPosition(
		functionCursor{Version: 1, Kind: "functions", AdapterID: adapterID, Revision: revision, Key: key},
	)
}

func DecodeFunctionCursor(adapterID, value string) (CatalogID, FunctionKey, error) {
	if value == "" {
		return "", "", nil
	}
	position, err := decodeFunctionPosition(value)
	if err != nil {
		return "", "", err
	}
	if position.Kind != "functions" || position.AdapterID != adapterID || !ValidFunctionKey(position.Key) ||
		validateID(string(position.Revision), "afc") != nil ||
		position.Time != "" ||
		position.ID != "" {
		return "", "", ErrInvalidFunctionInput
	}
	return position.Revision, position.Key, nil
}

func EncodeInvocationCursor(filter InvocationFilter, at time.Time, id InvocationID) string {
	return encodeFunctionPosition(
		functionCursor{Version: 1, Kind: "invocations", AdapterID: filter.AdapterID, Key: filter.FunctionKey,
			Time: at.UTC().Format("2006-01-02T15:04:05.000000000Z"), ID: id},
	)
}

func DecodeInvocationCursor(filter InvocationFilter) (time.Time, InvocationID, error) {
	if filter.Cursor == "" {
		return time.Time{}, "", nil
	}
	position, err := decodeFunctionPosition(filter.Cursor)
	if err != nil || position.Kind != "invocations" || position.AdapterID != filter.AdapterID ||
		position.Key != filter.FunctionKey ||
		position.Revision != "" ||
		validateID(string(position.ID), "afi") != nil {
		return time.Time{}, "", ErrInvalidFunctionInput
	}
	at, err := time.Parse("2006-01-02T15:04:05.000000000Z", position.Time)
	if err != nil || !ValidFunctionTime(at) {
		return time.Time{}, "", ErrInvalidFunctionInput
	}
	return at, position.ID, nil
}

func encodeFunctionPosition(position functionCursor) string {
	encoded, _ := json.Marshal(position)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeFunctionPosition(value string) (functionCursor, error) {
	const maximumCursorSize = 2048
	if len(value) > maximumCursorSize {
		return functionCursor{}, ErrInvalidFunctionInput
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return functionCursor{}, ErrInvalidFunctionInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var position functionCursor
	if err = decoder.Decode(&position); err != nil {
		return functionCursor{}, ErrInvalidFunctionInput
	}
	if decoder.Decode(&struct{}{}) != io.EOF || position.Version != 1 {
		return functionCursor{}, ErrInvalidFunctionInput
	}
	return position, nil
}
