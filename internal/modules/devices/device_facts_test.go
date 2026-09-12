package devices_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	factTestID            = "fct_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factTestEntityID      = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factTestObservationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factTestEventID       = "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factTestCommandID     = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factTestCorrelationID = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

// TestDeviceFactFamiliesMatchEmbeddedSchemas proves the subject family token is
// exactly the family of the embedded strict schema, and that no fourth fact
// schema exists without a subject family.
func TestDeviceFactFamiliesMatchEmbeddedSchemas(t *testing.T) {
	t.Parallel()
	families := []natswire.DeviceFactFamily{
		natswire.DeviceFactFamilyObservation,
		natswire.DeviceFactFamilyEntityEvent,
		natswire.DeviceFactFamilyCommand,
	}
	files := contractsv1.SchemaFiles()
	factSchemas := 0
	for schemaID := range files {
		if strings.HasSuffix(schemaID, "-fact:v1") {
			factSchemas++
		}
	}
	if factSchemas != len(families) {
		t.Fatalf("embedded %d fact schemas, want %d", factSchemas, len(families))
	}
	for _, family := range families {
		schemaID := "urn:hearth:schema:" + string(family) + "-fact:v1"
		if _, ok := files[schemaID]; !ok {
			t.Fatalf("subject family %q has no embedded fact schema %q", family, schemaID)
		}
	}
}

// TestDeviceFactVocabularyMatchesSchemasAndDomain keeps the subject constants,
// the strict schema enums and the devices domain values in one closed
// vocabulary, so drift in any of the three fails here.
func TestDeviceFactVocabularyMatchesSchemasAndDomain(t *testing.T) {
	t.Parallel()
	commandDocument := embeddedSchemaDocument(t, contractsv1.CommandFactSchemaID)
	assertSameStringSet(
		t,
		"Observation fact dispositions",
		[]string{string(devices.DispositionApplied), string(devices.DispositionUnchanged)},
		[]string{natswire.ObservationFactApplied, natswire.ObservationFactUnchanged},
		schemaEnum(t, embeddedSchemaDocument(t, contractsv1.ObservationFactSchemaID),
			"properties", "data", "properties", "disposition", "enum"),
	)
	assertSameStringSet(
		t,
		"Command fact statuses",
		[]string{
			string(devices.CommandStatusRequested),
			string(devices.CommandStatusAccepted),
			string(devices.CommandStatusSatisfied),
			string(devices.CommandStatusDispatched),
			string(devices.CommandStatusRejected),
			string(devices.CommandStatusAdapterUnhealthy),
			string(devices.CommandStatusEntityUnavailable),
			string(devices.CommandStatusOutcomeTimeout),
			string(devices.CommandStatusEntityDisabled),
			string(devices.CommandStatusInternalFailure),
			string(devices.CommandStatusInterrupted),
		},
		[]string{
			natswire.CommandFactRequested,
			natswire.CommandFactAccepted,
			natswire.CommandFactSatisfied,
			natswire.CommandFactDispatched,
			natswire.CommandFactRejected,
			natswire.CommandFactAdapterUnhealthy,
			natswire.CommandFactEntityUnavailable,
			natswire.CommandFactOutcomeTimeout,
			natswire.CommandFactEntityDisabled,
			natswire.CommandFactInternalFailure,
			natswire.CommandFactInterrupted,
		},
		schemaEnum(t, commandDocument, "properties", "data", "properties", "status", "enum"),
	)
	assertSameStringSet(
		t,
		"Command fact failure codes",
		[]string{
			string(devices.CommandFailureAdapterUnhealthy),
			string(devices.CommandFailureEntityUnavailable),
			string(devices.CommandFailureUpstreamRejected),
			string(devices.CommandFailureOutcomeTimeout),
			string(devices.CommandFailureEntityDisabled),
			string(devices.CommandFailureInternalError),
			string(devices.CommandFailureCoreRestarted),
		},
		schemaEnum(t, commandDocument, "properties", "data", "properties", "failure_code", "enum"),
	)
}

// deviceFactAgreementCase pairs one subject, built from devices domain values,
// with the payload a subscriber validates after receiving it.
type deviceFactAgreementCase struct {
	name         string
	entityID     string
	family       natswire.DeviceFactFamily
	variant      string
	variantField string
	schemaID     string
	payload      map[string]any
}

// TestDeviceFactSubjectsAgreeWithSchemaPayloads connects the subject a
// subscriber receives to the payload it validates, using the devices domain
// values that D2 will publish.
func TestDeviceFactSubjectsAgreeWithSchemaPayloads(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range deviceFactAgreementCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertDeviceFactAgreement(t, validator, test)
		})
	}
}

func assertDeviceFactAgreement(
	t *testing.T,
	validator *contractsv1.Validator,
	test deviceFactAgreementCase,
) {
	t.Helper()
	subject, subjectErr := buildDeviceFactSubject(test.entityID, test.family, test.variant)
	if subjectErr != nil {
		t.Fatal(subjectErr)
	}
	route, routeErr := natswire.ParseDeviceFactSubject(subject)
	if routeErr != nil {
		t.Fatalf("parse %q: %v", subject, routeErr)
	}
	encoded, encodeErr := json.Marshal(test.payload)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if validationErr := validator.Validate(test.schemaID, encoded); validationErr != nil {
		t.Fatalf("payload rejected: %v\npayload: %s", validationErr, encoded)
	}
	var decoded struct {
		Schema string         `json:"schema"`
		Data   map[string]any `json:"data"`
	}
	if decodeErr := json.Unmarshal(encoded, &decoded); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	schemaFamily := "urn:hearth:schema:" + string(route.Family) + "-fact:v1"
	if decoded.Schema != test.schemaID || decoded.Schema != schemaFamily {
		t.Fatalf("payload schema %q disagrees with schema id %q and subject family %q",
			decoded.Schema, test.schemaID, route.Family)
	}
	if decoded.Data["entity_id"] != route.EntityID {
		t.Fatalf("payload entity %v disagrees with subject entity %q",
			decoded.Data["entity_id"], route.EntityID)
	}
	if decoded.Data[test.variantField] != route.Variant {
		t.Fatalf("payload %s %v disagrees with subject variant %q",
			test.variantField, decoded.Data[test.variantField], route.Variant)
	}
}

func deviceFactAgreementCases() []deviceFactAgreementCase {
	return []deviceFactAgreementCase{
		{
			name:         "applied observation",
			entityID:     factTestEntityID,
			family:       natswire.DeviceFactFamilyObservation,
			variant:      string(devices.DispositionApplied),
			variantField: "disposition",
			schemaID:     contractsv1.ObservationFactSchemaID,
			payload: observationFactPayload(map[string]any{
				"observation_id":      factTestObservationID,
				"entity_id":           factTestEntityID,
				"disposition":         string(devices.DispositionApplied),
				"value":               map[string]any{"on": true},
				"adapter_received_at": "2026-08-20T12:34:55Z",
				"observed_at":         "2026-08-20T12:34:56Z",
			}),
		},
		{
			name:         "unchanged observation",
			entityID:     factTestEntityID,
			family:       natswire.DeviceFactFamilyObservation,
			variant:      string(devices.DispositionUnchanged),
			variantField: "disposition",
			schemaID:     contractsv1.ObservationFactSchemaID,
			payload: observationFactPayload(map[string]any{
				"observation_id":      factTestObservationID,
				"entity_id":           factTestEntityID,
				"disposition":         string(devices.DispositionUnchanged),
				"value":               false,
				"adapter_received_at": "2026-08-20T12:34:55Z",
				"observed_at":         "2026-08-20T12:34:56Z",
			}),
		},
		{
			name:         "accepted entity event",
			entityID:     factTestEntityID,
			family:       natswire.DeviceFactFamilyEntityEvent,
			variant:      "single_press",
			variantField: "name",
			schemaID:     contractsv1.EntityEventFactSchemaID,
			payload: entityEventFactPayload(map[string]any{
				"event_id":    factTestEventID,
				"entity_id":   factTestEntityID,
				"name":        "single_press",
				"reported_at": "2026-08-20T12:34:55.500Z",
				"received_at": "2026-08-20T12:34:56Z",
				"recorded_at": "2026-08-20T12:34:56Z",
			}),
		},
		{
			name:         "satisfied command",
			entityID:     factTestEntityID,
			family:       natswire.DeviceFactFamilyCommand,
			variant:      string(devices.CommandStatusSatisfied),
			variantField: "status",
			schemaID:     contractsv1.CommandFactSchemaID,
			payload: commandFactPayload(map[string]any{
				"command_id":             factTestCommandID,
				"entity_id":              factTestEntityID,
				"operation":              "set",
				"parameters":             map[string]any{"value": true},
				"status":                 string(devices.CommandStatusSatisfied),
				"requested_at":           "2026-08-20T12:34:56Z",
				"deadline_at":            "2026-08-20T12:35:06Z",
				"completed_at":           "2026-08-20T12:35:00Z",
				"outcome_observation_id": factTestObservationID,
			}),
		},
	}
}

func buildDeviceFactSubject(entityID string, family natswire.DeviceFactFamily, variant string) (string, error) {
	switch family {
	case natswire.DeviceFactFamilyObservation:
		return natswire.ObservationFactSubject(entityID, variant)
	case natswire.DeviceFactFamilyEntityEvent:
		return natswire.EntityEventFactSubject(entityID, variant)
	case natswire.DeviceFactFamilyCommand:
		return natswire.CommandFactSubject(entityID, variant)
	}
	return "", errors.New("unexpected Device Fact family")
}

func embeddedSchemaDocument(t *testing.T, schemaID string) map[string]any {
	t.Helper()
	path, ok := contractsv1.SchemaFiles()[schemaID]
	if !ok {
		t.Fatalf("schema %q is not registered", schemaID)
	}
	raw, err := contractsv1.FS.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if unmarshalErr := json.Unmarshal(raw, &document); unmarshalErr != nil {
		t.Fatalf("decode %s: %v", path, unmarshalErr)
	}
	return document
}

func schemaEnum(t *testing.T, document map[string]any, path ...string) []string {
	t.Helper()
	var value any = document
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("schema path %v is not an object", path)
		}
		value, ok = object[key]
		if !ok {
			t.Fatalf("schema path %v is missing %q", path, key)
		}
	}
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("schema path %v is not an enum array", path)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		text, isString := item.(string)
		if !isString {
			t.Fatalf("schema path %v contains a non-string enum value", path)
		}
		values = append(values, text)
	}
	return values
}

func assertSameStringSet(t *testing.T, name string, want []string, others ...[]string) {
	t.Helper()
	sortedWant := slices.Sorted(slices.Values(want))
	for index, values := range others {
		sortedValues := slices.Sorted(slices.Values(values))
		if !slices.Equal(sortedWant, sortedValues) {
			t.Fatalf("%s variant %d = %v, want %v", name, index, sortedValues, sortedWant)
		}
	}
}

func observationFactPayload(data map[string]any) map[string]any {
	return factTestEnvelope(contractsv1.ObservationFactSchemaID, factTestObservationID, data)
}

func entityEventFactPayload(data map[string]any) map[string]any {
	return factTestEnvelope(contractsv1.EntityEventFactSchemaID, factTestEventID, data)
}

func commandFactPayload(data map[string]any) map[string]any {
	return factTestEnvelope(contractsv1.CommandFactSchemaID, factTestCommandID, data)
}

func factTestEnvelope(schemaID, causationID string, data map[string]any) map[string]any {
	return map[string]any{
		"id":             factTestID,
		"schema":         schemaID,
		"emitted_at":     "2026-08-20T12:34:56Z",
		"correlation_id": factTestCorrelationID,
		"causation_id":   causationID,
		"data":           data,
	}
}
