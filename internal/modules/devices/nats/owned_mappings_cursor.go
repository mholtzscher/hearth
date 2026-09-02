package nats

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	ownedMappingCursorVersion  = 1
	ownedMappingCursorResource = "adapter_owned_mappings"
)

var ownedMappingCursorSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type ownedMappingCursor struct {
	Version    int    `json:"v"`
	Resource   string `json:"resource"`
	AdapterID  string `json:"adapter_id"`
	BindingKey string `json:"binding_key"`
	EntityKey  string `json:"entity_key"`
}

func encodeOwnedMappingCursor(adapterID string, position devices.OwnedMappingPosition) (string, error) {
	if !ownedMappingCursorSlugPattern.MatchString(adapterID) ||
		!ownedMappingCursorSlugPattern.MatchString(position.BindingKey) ||
		!ownedMappingCursorSlugPattern.MatchString(position.EntityKey) {
		return "", errors.New("invalid owned mapping cursor position")
	}
	encoded, err := json.Marshal(ownedMappingCursor{
		Version: ownedMappingCursorVersion, Resource: ownedMappingCursorResource,
		AdapterID: adapterID, BindingKey: position.BindingKey, EntityKey: position.EntityKey,
	})
	if err != nil {
		return "", fmt.Errorf("encode owned mapping cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeOwnedMappingCursor(value, adapterID string) (*devices.OwnedMappingPosition, error) {
	if value == "" {
		return nil, errors.New("owned mapping cursor is empty")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode owned mapping cursor: %w", err)
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("owned mapping cursor encoding is not canonical")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var cursor ownedMappingCursor
	if decodeErr := decoder.Decode(&cursor); decodeErr != nil {
		return nil, fmt.Errorf("decode owned mapping cursor document: %w", decodeErr)
	}
	if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
		return nil, errors.New("owned mapping cursor contains trailing JSON")
	}
	if cursor.Version != ownedMappingCursorVersion || cursor.Resource != ownedMappingCursorResource ||
		cursor.AdapterID != adapterID || !ownedMappingCursorSlugPattern.MatchString(cursor.BindingKey) ||
		!ownedMappingCursorSlugPattern.MatchString(cursor.EntityKey) {
		return nil, errors.New("invalid owned mapping cursor scope or position")
	}
	return &devices.OwnedMappingPosition{
		BindingKey: cursor.BindingKey,
		EntityKey:  cursor.EntityKey,
	}, nil
}
