package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and cursor validation.

import (
	"context"
	"encoding/base64"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type ownedMappingListerFunc func(
	context.Context,
	string,
	adapters.RuntimeID,
	devices.OwnedMappingPageParams,
) (devices.Page[devices.OwnedMapping], error)

func (lister ownedMappingListerFunc) ListOwnedMappings(
	ctx context.Context,
	adapterID string,
	runtimeID adapters.RuntimeID,
	page devices.OwnedMappingPageParams,
) (devices.Page[devices.OwnedMapping], error) {
	return lister(ctx, adapterID, runtimeID, page)
}

func TestOwnedMappingsCursorRoundTripAndStrictValidation(t *testing.T) {
	t.Parallel()
	position := devices.OwnedMappingPosition{BindingKey: "office-light", EntityKey: "power"}
	valid, err := encodeOwnedMappingCursor("simulator", position)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeOwnedMappingCursor(valid, "simulator")
	if err != nil || !reflect.DeepEqual(*decoded, position) {
		t.Fatalf("decoded cursor = %#v, error = %v", decoded, err)
	}

	malformedJSON := encodeOwnedMappingCursorDocument(`{"v":`)
	trailingJSON := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"adapter_owned_mappings","adapter_id":"simulator","binding_key":"office-light","entity_key":"power"}{}`,
	)
	wrongVersion := encodeOwnedMappingCursorDocument(
		`{"v":2,"resource":"adapter_owned_mappings","adapter_id":"simulator","binding_key":"office-light","entity_key":"power"}`,
	)
	wrongResource := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"devices","adapter_id":"simulator","binding_key":"office-light","entity_key":"power"}`,
	)
	wrongAdapter := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"adapter_owned_mappings","adapter_id":"other","binding_key":"office-light","entity_key":"power"}`,
	)
	invalidBindingKey := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"adapter_owned_mappings","adapter_id":"simulator","binding_key":"Office Light","entity_key":"power"}`,
	)
	invalidEntityKey := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"adapter_owned_mappings","adapter_id":"simulator","binding_key":"office-light","entity_key":""}`,
	)

	for name, value := range map[string]string{
		"empty":               "",
		"malformed base64url": "not base64!",
		"padded base64url":    valid + "=",
		"noncanonical":        noncanonicalBase64URL(t, valid),
		"malformed JSON":      malformedJSON,
		"trailing JSON":       trailingJSON,
		"wrong version":       wrongVersion,
		"wrong resource":      wrongResource,
		"wrong Adapter scope": wrongAdapter,
		"invalid Binding key": invalidBindingKey,
		"invalid Entity key":  invalidEntityKey,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, decodeErr := decodeOwnedMappingCursor(value, "simulator"); decodeErr == nil {
				t.Fatalf("decodeOwnedMappingCursor(%q) succeeded", value)
			}
		})
	}
}

func TestOwnedMappingsServerEnforcesCursorByteLimitBeforeListing(t *testing.T) {
	t.Parallel()
	subject, err := natswire.OwnedMappingsSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	lister := ownedMappingListerFunc(func(
		context.Context,
		string,
		adapters.RuntimeID,
		devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		called.Store(true)
		return devices.Page[devices.OwnedMapping]{}, nil
	})
	logger := slog.New(slog.DiscardHandler)
	atLimit, handled := handleOwnedMappings(
		context.Background(), subject,
		natswire.Envelope[ownedMappingsRequest]{
			ID: ownedMappingsRequestID,
			Data: ownedMappingsRequest{
				Cursor: strings.Repeat("a", maximumOwnedMappingsCursorBytes),
			},
		},
		lister, logger,
	)
	if !handled || atLimit.Error == nil || atLimit.Error.Code != ownedMappingsInvalidCursorCode {
		t.Fatalf("cursor at byte limit response = %#v, handled = %t", atLimit, handled)
	}

	oversized, handled := handleOwnedMappings(
		context.Background(), subject,
		natswire.Envelope[ownedMappingsRequest]{
			ID: ownedMappingsRequestID,
			Data: ownedMappingsRequest{
				Cursor: strings.Repeat("é", maximumOwnedMappingsCursorBytes/2+1),
			},
		},
		lister, logger,
	)
	if !handled || called.Load() || oversized.Error == nil ||
		oversized.Error.Code != ownedMappingsInvalidCursorCode {
		t.Fatalf("oversized cursor response = %#v, handled = %t, called = %t", oversized, handled, called.Load())
	}
}

const ownedMappingsRequestID = "map_01890f47-7a6b-7c4d-8e9f-0123456789ab"

func validOwnedMappingsEnvelope(data ownedMappingsRequest) natswire.Envelope[ownedMappingsRequest] {
	return natswire.Envelope[ownedMappingsRequest]{
		ID: ownedMappingsRequestID, Schema: contractsv1.OwnedMappingsRequestSchemaID,
		EmittedAt: "2026-09-01T12:00:00Z", CorrelationID: testCorrelationID, Data: data,
	}
}

func encodeOwnedMappingCursorDocument(document string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(document))
}

func noncanonicalBase64URL(t *testing.T, canonical string) string {
	t.Helper()
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	remainder := len(canonical) % 4
	if remainder != 2 && remainder != 3 {
		t.Fatalf("fixture has no unused base64 bits: %q", canonical)
	}
	index := -1
	for candidate := range len(alphabet) {
		if alphabet[candidate] == canonical[len(canonical)-1] {
			index = candidate
			break
		}
	}
	if index < 0 || index&1 != 0 {
		t.Fatalf("unexpected canonical final base64 character %q", canonical[len(canonical)-1])
	}
	return canonical[:len(canonical)-1] + string(alphabet[index|1])
}
