package v1_test

import (
	"encoding/json"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

const (
	factID       = "fct_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factObsID    = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factEvtID    = "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factCorID    = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

func TestDeviceFactSchemasAreRegistered(t *testing.T) {
	t.Parallel()
	files := contractsv1.SchemaFiles()
	registered := map[string]string{
		contractsv1.ObservationFactSchemaID: "observation-fact.schema.json",
		contractsv1.EntityEventFactSchemaID: "entity-event-fact.schema.json",
	}
	for schemaID, file := range registered {
		if files[schemaID] != file {
			t.Fatalf("SchemaFiles()[%q] = %q, want %q", schemaID, files[schemaID], file)
		}
	}
}

// TestObservationFactSchemaFixtures covers the accepted Observation wire
// contract: only applied and unchanged dispositions exist, and source_updated_at
// is optional.
func TestObservationFactSchemaFixtures(t *testing.T) {
	t.Parallel()
	validator := newDeviceFactValidator(t)
	tests := []struct {
		name      string
		mutate    func(payload map[string]any)
		wantValid bool
	}{
		{name: "applied observation with source update", wantValid: true},
		{
			name:      "unchanged observation without source update",
			mutate:    func(payload map[string]any) { deleteDataField(payload, "source_updated_at") },
			wantValid: true,
		},
		{
			name: "rejected disposition is not a fact",
			mutate: func(payload map[string]any) {
				setDataField(payload, "disposition", "rejected")
				setDataField(payload, "rejection_code", "unknown_entity")
			},
		},
		{
			name:   "duplicate disposition is not a fact",
			mutate: func(payload map[string]any) { setDataField(payload, "disposition", "duplicate") },
		},
		{
			name:   "causation must be the observation",
			mutate: func(payload map[string]any) { payload["causation_id"] = factEvtID },
		},
		{
			name:   "another fact id is not a causation id",
			mutate: func(payload map[string]any) { payload["causation_id"] = factID },
		},
		{
			name:   "causation is required",
			mutate: func(payload map[string]any) { delete(payload, "causation_id") },
		},
		{
			name:   "fact id must use the fct prefix",
			mutate: func(payload map[string]any) { payload["id"] = factObsID },
		},
		{
			name:   "fact id must be a version 7 UUID",
			mutate: func(payload map[string]any) { payload["id"] = "fct_01890f47-7a6b-4c4d-8e9f-0123456789ab" },
		},
		{
			name:   "fact id must be canonical lowercase",
			mutate: func(payload map[string]any) { payload["id"] = "fct_01890F47-7A6B-7C4D-8E9F-0123456789AB" },
		},
		{
			name:   "emitted_at must be UTC",
			mutate: func(payload map[string]any) { payload["emitted_at"] = "2026-08-20T12:34:56+00:00" },
		},
		{
			name:   "unknown envelope property is rejected",
			mutate: func(payload map[string]any) { payload["nats_msg_id"] = "ignored" },
		},
		{
			name:   "unknown data property is rejected",
			mutate: func(payload map[string]any) { setDataField(payload, "adapter_received_at_extra", "ignored") },
		},
		{
			name:   "observation id is required",
			mutate: func(payload map[string]any) { deleteDataField(payload, "observation_id") },
		},
		{
			name:   "value is required",
			mutate: func(payload map[string]any) { deleteDataField(payload, "value") },
		},
		{
			name:   "core observed_at is required",
			mutate: func(payload map[string]any) { deleteDataField(payload, "observed_at") },
		},
		{
			name:   "entity id must be canonical",
			mutate: func(payload map[string]any) { setDataField(payload, "entity_id", "ent_not-a-uuid") },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := observationFactPayload()
			if test.mutate != nil {
				test.mutate(payload)
			}
			validateDeviceFact(t, validator, contractsv1.ObservationFactSchemaID, payload, test.wantValid)
		})
	}
}

// TestEntityEventFactSchemaFixtures covers the accepted Entity Event wire
// contract: no disposition exists and all three Core and Adapter timestamps are
// explicit.
func TestEntityEventFactSchemaFixtures(t *testing.T) {
	t.Parallel()
	validator := newDeviceFactValidator(t)
	tests := []struct {
		name      string
		mutate    func(payload map[string]any)
		wantValid bool
	}{
		{name: "accepted entity event", wantValid: true},
		{
			name:   "status is not part of an entity event fact",
			mutate: func(payload map[string]any) { setDataField(payload, "disposition", "accepted") },
		},
		{
			name:   "causation must be the entity event",
			mutate: func(payload map[string]any) { payload["causation_id"] = factObsID },
		},
		{
			name:   "recorded_at is required",
			mutate: func(payload map[string]any) { deleteDataField(payload, "recorded_at") },
		},
		{
			name:   "reported_at is required",
			mutate: func(payload map[string]any) { deleteDataField(payload, "reported_at") },
		},
		{
			name:   "name must be a slug",
			mutate: func(payload map[string]any) { setDataField(payload, "name", "Single Press") },
		},
		{
			name:   "event id must use the evt prefix",
			mutate: func(payload map[string]any) { setDataField(payload, "event_id", factObsID) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := entityEventFactPayload()
			if test.mutate != nil {
				test.mutate(payload)
			}
			validateDeviceFact(t, validator, contractsv1.EntityEventFactSchemaID, payload, test.wantValid)
		})
	}
}

func newDeviceFactValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func validateDeviceFact(
	t *testing.T,
	validator *contractsv1.Validator,
	schemaID string,
	payload map[string]any,
	wantValid bool,
) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	validationErr := validator.Validate(schemaID, encoded)
	if wantValid && validationErr != nil {
		t.Fatalf("payload rejected: %v\npayload: %s", validationErr, encoded)
	}
	if !wantValid && validationErr == nil {
		t.Fatalf("payload unexpectedly accepted: %s", encoded)
	}
}

func factEnvelope(schemaID, causationID string, data map[string]any) map[string]any {
	return map[string]any{
		"id":             factID,
		"schema":         schemaID,
		"emitted_at":     "2026-08-20T12:34:56Z",
		"correlation_id": factCorID,
		"causation_id":   causationID,
		"data":           data,
	}
}

func observationFactPayload() map[string]any {
	return factEnvelope(contractsv1.ObservationFactSchemaID, factObsID, map[string]any{
		"observation_id":      factObsID,
		"entity_id":           factEntityID,
		"disposition":         "applied",
		"value":               map[string]any{"on": true},
		"adapter_received_at": "2026-08-20T12:34:55Z",
		"source_updated_at":   "2026-08-20T12:34:54Z",
		"observed_at":         "2026-08-20T12:34:56Z",
	})
}

func entityEventFactPayload() map[string]any {
	return factEnvelope(contractsv1.EntityEventFactSchemaID, factEvtID, map[string]any{
		"event_id":    factEvtID,
		"entity_id":   factEntityID,
		"name":        "single_press",
		"reported_at": "2026-08-20T12:34:55.500Z",
		"received_at": "2026-08-20T12:34:56Z",
		"recorded_at": "2026-08-20T12:34:56Z",
	})
}

func setDataField(payload map[string]any, key string, value any) {
	data, ok := payload["data"].(map[string]any)
	if !ok {
		panic("payload data is not an object")
	}
	data[key] = value
}

func deleteDataField(payload map[string]any, key string) {
	data, ok := payload["data"].(map[string]any)
	if !ok {
		panic("payload data is not an object")
	}
	delete(data, key)
}
