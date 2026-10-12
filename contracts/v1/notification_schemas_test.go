package v1_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

const notificationUUID = "01890f47-7a6b-7c4d-8e9f-0123456789ab"

//nolint:gocognit // The four schema alternatives share one external-input boundary matrix.
func TestNotificationSchemasEnforceStrictAlternativesAndBounds(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, schema, prefix, cause string
		data                        map[string]any
		invalid                     []map[string]any
	}{
		{
			name: "registration request", schema: contractsv1.NotificationRegistrationRequestSchemaID, prefix: "reg",
			data: map[string]any{"key": "household", "name": strings.Repeat("界", 200)},
			invalid: []map[string]any{
				{"key": nil, "name": "Household"}, {"key": "bad.key", "name": "Household"},
				{"key": "household", "name": nil}, {"key": "household", "name": " "},
				{"key": "household", "name": "\u00a0Household\u00a0"},
				{"key": "household", "name": " padded "}, {"key": "household", "name": strings.Repeat("界", 201)},
				{"key": "household", "name": "Household", "secret": "forbidden"},
			},
		},
		{
			name:   "registration response",
			schema: contractsv1.NotificationRegistrationResponseSchemaID,
			prefix: "rep",
			cause:  "reg",
			data:   map[string]any{"status": "accepted", "destination_id": "ndst_" + notificationUUID},
			invalid: []map[string]any{
				{"status": "accepted"}, {"status": "accepted", "destination_id": nil},
				{"status": "accepted", "destination_id": "ndst_" + notificationUUID, "reason_code": "stale_runtime"},
				{"status": "rejected", "reason_code": "unrecognized"},
				{"status": "rejected", "reason_code": "stale_runtime", "destination_id": "ndst_" + notificationUUID},
			},
		},
		{
			name:   "send request",
			schema: contractsv1.NotificationSendRequestSchemaID,
			prefix: "ntf",
			data: map[string]any{
				"attempt_id": "ntf_" + notificationUUID,
				"local_key":  "household",
				"title":      strings.Repeat("界", 256),
				"message":    strings.Repeat("界", 2000),
				"deadline":   "2026-10-11T12:00:00Z",
			},
			invalid: []map[string]any{
				{"title": nil},
				{"title": strings.Repeat("界", 257)},
				{"message": nil},
				{"message": " \t\n"},
				{"message": "\u00a0\u2000"},
				{"message": strings.Repeat("界", 2001)},
				{"deadline": "2026-10-11T12:00:00+00:00"},
				{
					"deadline": "not-a-time",
				},
				{"local_key": "*"},
				{"secret": "forbidden"},
				{"attempt_id": "cmd_" + notificationUUID},
			},
		},
		{
			name:   "send response",
			schema: contractsv1.NotificationSendResponseSchemaID,
			prefix: "rep",
			cause:  "ntf",
			data: map[string]any{
				"attempt_id": "ntf_" + notificationUUID,
				"outcome":    "accepted",
				"receipt":    strings.Repeat("界", 256),
			},
			invalid: []map[string]any{
				{"outcome": "accepted", "reason_code": "provider_error"},
				{"outcome": "accepted", "receipt": nil},
				{"outcome": "accepted", "receipt": ""},
				{"outcome": "accepted", "receipt": strings.Repeat("界", 257)},
				{"outcome": "unknown"},
				{"outcome": "unknown", "reason_code": "arbitrary_code"},
				{"outcome": "unknown", "reason_code": "provider_error", "receipt": "123"},
				{
					"outcome":     "rejected",
					"reason_code": nil,
				},
				{"outcome": "not_sent", "reason_code": "adapter_busy", "extra": true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			envelope := map[string]any{
				"id":             tt.prefix + "_" + notificationUUID,
				"schema":         tt.schema,
				"emitted_at":     "2026-10-11T12:00:00Z",
				"correlation_id": "cor_" + notificationUUID,
				"data":           tt.data,
			}
			if tt.cause != "" {
				envelope["causation_id"] = tt.cause + "_" + notificationUUID
			}
			validate := func(wantValid bool) {
				t.Helper()
				payload, marshalErr := json.Marshal(envelope)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				validationErr := validator.Validate(tt.schema, payload)
				if (validationErr == nil) != wantValid {
					t.Fatalf("valid=%v, data=%v, error=%v", wantValid, envelope["data"], validationErr)
				}
			}
			validate(true)
			for _, invalid := range tt.invalid {
				data := make(map[string]any)
				// Send mutations preserve the required route/deadline fields so
				// rejection tests the named defect, not an unrelated omission.
				if tt.name == "send request" {
					maps.Copy(data, tt.data)
				}
				if tt.name == "send response" {
					data["attempt_id"] = "ntf_" + notificationUUID
				}
				maps.Copy(data, invalid)
				envelope["data"] = data
				validate(false)
			}
			envelope["data"] = tt.data
			envelope["unexpected"] = true
			validate(false)
		})
	}
}

func TestNotificationSchemasAcceptOmissionAndTerminalEvidence(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"accepted", "rejected", "not_sent", "unknown"} {
		data := map[string]any{"attempt_id": "ntf_" + notificationUUID, "outcome": outcome}
		switch outcome {
		case "rejected":
			data["reason_code"] = "destination_unknown"
		case "not_sent":
			data["reason_code"] = "adapter_busy"
		case "unknown":
			data["reason_code"] = "invalid_response"
		}
		payload, marshalErr := json.Marshal(
			map[string]any{
				"id":             "rep_" + notificationUUID,
				"schema":         contractsv1.NotificationSendResponseSchemaID,
				"emitted_at":     "2026-10-11T12:00:00Z",
				"correlation_id": "cor_" + notificationUUID,
				"causation_id":   "ntf_" + notificationUUID,
				"data":           data,
			},
		)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if validateErr := validator.Validate(
			contractsv1.NotificationSendResponseSchemaID,
			payload,
		); validateErr != nil {
			t.Fatal(validateErr)
		}
	}
	for _, title := range []string{"", "omitted"} {
		data := map[string]any{
			"attempt_id": "ntf_" + notificationUUID,
			"local_key":  "household",
			"message":    "Alert",
			"deadline":   "2026-10-11T12:00:00Z",
		}
		if title == "" {
			data["title"] = title
		}
		payload, marshalErr := json.Marshal(
			map[string]any{"id": "ntf_" + notificationUUID, "schema": contractsv1.NotificationSendRequestSchemaID,
				"emitted_at": "2026-10-11T12:00:00Z", "correlation_id": "cor_" + notificationUUID, "data": data},
		)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if validateErr := validator.Validate(contractsv1.NotificationSendRequestSchemaID, payload); validateErr != nil {
			t.Fatal(validateErr)
		}
		payload = []byte(strings.Replace(string(payload), "Alert", string([]byte{0xff}), 1))
		if validateErr := validator.Validate(contractsv1.NotificationSendRequestSchemaID, payload); validateErr == nil {
			t.Fatal("invalid UTF-8 accepted")
		}
	}
}
