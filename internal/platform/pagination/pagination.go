// Package pagination owns the shared HTTP cursor envelope. It encodes cursor
// documents as canonical unpadded base64url JSON and rejects unknown fields,
// trailing JSON, and non-canonical encodings. Endpoint scope, filter binding,
// and position validation stay with the calling module.
package pagination

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// EncodeCursor serializes one cursor document into its opaque wire form.
func EncodeCursor[T any](cursor T) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

// DecodeCursor parses one opaque cursor into its document type. It rejects an
// empty value, non-base64url input, non-canonical encodings, unknown fields,
// and trailing JSON, mirroring the previous per-module codecs byte for byte.
func DecodeCursor[T any](value string) (T, error) {
	var cursor T
	if value == "" {
		return cursor, errors.New("cursor is empty")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, fmt.Errorf("decode cursor: %w", err)
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != value {
		return cursor, errors.New("cursor encoding is not canonical")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&cursor); decodeErr != nil {
		return cursor, fmt.Errorf("decode cursor document: %w", decodeErr)
	}
	if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
		return cursor, errors.New("cursor contains trailing JSON")
	}
	return cursor, nil
}
