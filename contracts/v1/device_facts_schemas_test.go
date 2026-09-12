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
	factCmdID    = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	factCorID    = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

func TestDeviceFactSchemasAreRegistered(t *testing.T) {
	t.Parallel()
	files := contractsv1.SchemaFiles()
	registered := map[string]string{
		contractsv1.ObservationFactSchemaID: "observation-fact.schema.json",
		contractsv1.EntityEventFactSchemaID: "entity-event-fact.schema.json",
		contractsv1.CommandFactSchemaID:     "command-fact.schema.json",
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
			mutate: func(payload map[string]any) { payload["causation_id"] = factCmdID },
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

// TestCommandFactSchemaFixtures covers every Command status variant and the
// commands-table field invariants each status implies.
func TestCommandFactSchemaFixtures(t *testing.T) {
	t.Parallel()
	validator := newDeviceFactValidator(t)
	statusMutate := func(status string, extra map[string]any) func(payload map[string]any) {
		return func(payload map[string]any) {
			setDataField(payload, "status", status)
			for key, value := range extra {
				setDataField(payload, key, value)
			}
			for _, key := range []string{"completed_at", "failure_code", "outcome_observation_id"} {
				if _, present := extra[key]; !present {
					deleteDataField(payload, key)
				}
			}
		}
	}
	tests := []struct {
		name      string
		mutate    func(payload map[string]any)
		wantValid bool
	}{
		{name: "requested command", mutate: statusMutate("requested", nil), wantValid: true},
		{
			name: "accepted command",
			mutate: func(payload map[string]any) {
				statusMutate("accepted", nil)(payload)
				setDataField(payload, "accepted_at", "2026-08-20T12:34:57Z")
			},
			wantValid: true,
		},
		{
			name: "satisfied command keeps its outcome observation",
			mutate: statusMutate("satisfied", map[string]any{
				"completed_at":           "2026-08-20T12:35:00Z",
				"outcome_observation_id": factObsID,
			}),
			wantValid: true,
		},
		{
			name: "dispatched command has no outcome evidence",
			mutate: statusMutate("dispatched", map[string]any{
				"completed_at": "2026-08-20T12:34:58Z",
			}),
			wantValid: true,
		},
		{
			name: "rejected command",
			mutate: statusMutate("rejected", map[string]any{
				"completed_at": "2026-08-20T12:34:58Z",
				"failure_code": "upstream_rejected",
			}),
			wantValid: true,
		},
		{
			name: "adapter unhealthy command",
			mutate: statusMutate("adapter_unhealthy", map[string]any{
				"completed_at": "2026-08-20T12:34:58Z",
				"failure_code": "adapter_unhealthy",
			}),
			wantValid: true,
		},
		{
			name: "entity unavailable command",
			mutate: statusMutate("entity_unavailable", map[string]any{
				"completed_at": "2026-08-20T12:34:58Z",
				"failure_code": "entity_unavailable",
			}),
			wantValid: true,
		},
		{
			name: "outcome timeout command",
			mutate: statusMutate("outcome_timeout", map[string]any{
				"completed_at": "2026-08-20T12:35:06Z",
				"failure_code": "outcome_timeout",
			}),
			wantValid: true,
		},
		{
			name: "entity disabled command",
			mutate: statusMutate("entity_disabled", map[string]any{
				"completed_at": "2026-08-20T12:34:56Z",
				"failure_code": "entity_disabled",
			}),
			wantValid: true,
		},
		{
			name: "internal failure command",
			mutate: statusMutate("internal_failure", map[string]any{
				"completed_at": "2026-08-20T12:34:58Z",
				"failure_code": "internal_error",
			}),
			wantValid: true,
		},
		{
			name: "interrupted command",
			mutate: statusMutate("interrupted", map[string]any{
				"completed_at": "2026-08-20T12:34:59Z",
				"failure_code": "core_restarted",
			}),
			wantValid: true,
		},
		{
			name: "requested omits completed_at",
			mutate: func(payload map[string]any) {
				statusMutate("requested", nil)(payload)
				setDataField(payload, "completed_at", "2026-08-20T12:34:58Z")
			},
		},
		{
			name: "requested omits failure_code",
			mutate: func(payload map[string]any) {
				statusMutate("requested", nil)(payload)
				setDataField(payload, "failure_code", "internal_error")
			},
		},
		{
			name: "nonterminal command omits outcome evidence",
			mutate: func(payload map[string]any) {
				statusMutate("accepted", nil)(payload)
				setDataField(payload, "outcome_observation_id", factObsID)
			},
		},
		{
			name: "satisfied requires its outcome observation",
			mutate: statusMutate("satisfied", map[string]any{
				"completed_at": "2026-08-20T12:35:00Z",
			}),
		},
		{
			name: "satisfied requires completed_at",
			mutate: statusMutate("satisfied", map[string]any{
				"outcome_observation_id": factObsID,
			}),
		},
		{
			name: "satisfied omits failure_code",
			mutate: statusMutate("satisfied", map[string]any{
				"completed_at":           "2026-08-20T12:35:00Z",
				"outcome_observation_id": factObsID,
				"failure_code":           "entity_unavailable",
			}),
		},
		{
			name:   "dispatched requires completed_at",
			mutate: statusMutate("dispatched", nil),
		},
		{
			name: "dispatched omits failure_code",
			mutate: statusMutate("dispatched", map[string]any{
				"completed_at": "2026-08-20T12:34:58Z",
				"failure_code": "internal_error",
			}),
		},
		{
			name: "rejected requires completed_at",
			mutate: statusMutate("rejected", map[string]any{
				"failure_code": "upstream_rejected",
			}),
		},
		{
			name:   "rejected requires its failure code",
			mutate: statusMutate("rejected", map[string]any{"completed_at": "2026-08-20T12:34:58Z"}),
		},
		{
			name: "rejected rejects a mismatched failure code",
			mutate: statusMutate("rejected", map[string]any{
				"completed_at": "2026-08-20T12:34:58Z",
				"failure_code": "outcome_timeout",
			}),
		},
		{
			name: "interrupted requires the core restart failure code",
			mutate: statusMutate("interrupted", map[string]any{
				"completed_at": "2026-08-20T12:34:59Z",
				"failure_code": "internal_error",
			}),
		},
		{
			name: "internal failure requires the internal error failure code",
			mutate: statusMutate("internal_failure", map[string]any{
				"completed_at": "2026-08-20T12:34:58Z",
				"failure_code": "core_restarted",
			}),
		},
		{
			name: "failure command omits outcome evidence",
			mutate: statusMutate("adapter_unhealthy", map[string]any{
				"completed_at":           "2026-08-20T12:34:58Z",
				"failure_code":           "adapter_unhealthy",
				"outcome_observation_id": factObsID,
			}),
		},
		{
			name:   "unknown status is rejected",
			mutate: statusMutate("cancelled", nil),
		},
		{
			name:   "parameters must be an object",
			mutate: func(payload map[string]any) { setDataField(payload, "parameters", []any{true}) },
		},
		{
			name:   "parameters are required",
			mutate: func(payload map[string]any) { deleteDataField(payload, "parameters") },
		},
		{
			name:   "operation must be a slug",
			mutate: func(payload map[string]any) { setDataField(payload, "operation", "Set") },
		},
		{
			name:   "command id is required",
			mutate: func(payload map[string]any) { deleteDataField(payload, "command_id") },
		},
		{
			name: "outcome observation must be an observation",
			mutate: statusMutate("satisfied", map[string]any{
				"completed_at":           "2026-08-20T12:35:00Z",
				"outcome_observation_id": factEvtID,
			}),
		},
		{
			name:   "causation must be the command",
			mutate: func(payload map[string]any) { payload["causation_id"] = factObsID },
		},
		{
			name: "runtime identity is not part of a command fact",
			mutate: func(payload map[string]any) {
				setDataField(payload, "adapter_id", "zigbee2mqtt")
				setDataField(payload, "runtime_id", "run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := commandFactPayload()
			if test.mutate != nil {
				test.mutate(payload)
			}
			validateDeviceFact(t, validator, contractsv1.CommandFactSchemaID, payload, test.wantValid)
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

func commandFactPayload() map[string]any {
	return factEnvelope(contractsv1.CommandFactSchemaID, factCmdID, map[string]any{
		"command_id":   factCmdID,
		"entity_id":    factEntityID,
		"operation":    "set",
		"parameters":   map[string]any{"value": true},
		"status":       "requested",
		"requested_at": "2026-08-20T12:34:56Z",
		"deadline_at":  "2026-08-20T12:35:06Z",
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
