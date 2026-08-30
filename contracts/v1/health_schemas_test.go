package v1_test

import (
	"encoding/json"
	"fmt"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

const (
	testCorrelationID = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testRuntimeID     = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testEntityID      = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

func TestAdapterSessionAndAvailabilitySchemaFixtures(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		contractsv1.AdapterClaimRequestSchemaID: `{
			"id":"clm_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:adapter-claim-request:v1",
			"emitted_at":"2026-08-29T15:00:00Z",
			"correlation_id":"` + testCorrelationID + `",
			"data":{"adapter_id":"homeassistant","software_name":"hearth-adapter-homeassistant","software_version":"0.1.0"}
		}`,
		contractsv1.AdapterClaimResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:adapter-claim-response:v1",
			"emitted_at":"2026-08-29T15:00:00Z",
			"correlation_id":"` + testCorrelationID + `",
			"causation_id":"clm_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"status":"accepted","runtime_id":"` + testRuntimeID + `","heartbeat_interval_ms":5000,"lease_duration_ms":15000}
		}`,
		contractsv1.AdapterHeartbeatRequestSchemaID: `{
			"id":"hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:adapter-heartbeat-request:v1",
			"emitted_at":"2026-08-29T15:00:01Z",
			"correlation_id":"` + testCorrelationID + `",
			"data":{"external_system":{"status":"unhealthy","source_observed_at":"2026-08-29T15:00:00Z","reason":{"code":"hearth.network_unreachable","detail":"network is unreachable"}}}
		}`,
		contractsv1.AdapterHeartbeatResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ac",
			"schema":"urn:hearth:schema:adapter-heartbeat-response:v1",
			"emitted_at":"2026-08-29T15:00:01Z",
			"correlation_id":"` + testCorrelationID + `",
			"causation_id":"hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"status":"accepted","lease_expires_at":"2026-08-29T15:00:16Z","refresh_entity_availability":true}
		}`,
		contractsv1.AdapterReleaseRequestSchemaID: `{
			"id":"rel_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:adapter-release-request:v1",
			"emitted_at":"2026-08-29T15:01:00Z",
			"correlation_id":"` + testCorrelationID + `",
			"data":{}
		}`,
		contractsv1.AdapterReleaseResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ad",
			"schema":"urn:hearth:schema:adapter-release-response:v1",
			"emitted_at":"2026-08-29T15:01:00Z",
			"correlation_id":"` + testCorrelationID + `",
			"causation_id":"rel_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"status":"accepted"}
		}`,
		contractsv1.EntityAvailabilityRequestSchemaID: availabilityRequestFixture(),
		contractsv1.EntityAvailabilityResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ae",
			"schema":"urn:hearth:schema:entity-availability-response:v1",
			"emitted_at":"2026-08-29T15:00:01Z",
			"correlation_id":"` + testCorrelationID + `",
			"causation_id":"avl_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"status":"accepted","reported_at":"2026-08-29T15:00:01Z","count":1}
		}`,
	}
	for schemaID, fixture := range fixtures {
		if validationErr := validator.Validate(schemaID, []byte(fixture)); validationErr != nil {
			t.Errorf("%s fixture: %v", schemaID, validationErr)
		}
	}
}

func TestNewRequestIDsParticipateInCausationUnion(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.CommandRequestSchemaID]
	request := decodeObject(t, `{
		"id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:command-request:v1",
		"emitted_at":"2026-08-29T15:00:01Z",
		"correlation_id":"`+testCorrelationID+`",
		"data":{"entity_id":"`+testEntityID+`","operation":"set","parameters":{"value":true},"deadline":"2026-08-29T15:00:11Z"}
	}`)
	for _, causationID := range []string{
		"clm_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"rel_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"avl_01890f47-7a6b-7c4d-8e9f-0123456789ab",
	} {
		request["causation_id"] = causationID
		if err := schema.Validate(request); err != nil {
			t.Errorf("causation ID %q rejected: %v", causationID, err)
		}
	}
}

func TestHealthSchemasEnforceStatusReasonBranches(t *testing.T) {
	t.Parallel()
	schemas := compileSchemas(t)
	heartbeat := decodeObject(t, `{
		"id":"hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:adapter-heartbeat-request:v1",
		"emitted_at":"2026-08-29T15:00:01Z",
		"correlation_id":"`+testCorrelationID+`",
		"data":{"external_system":{"status":"unhealthy","source_observed_at":"2026-08-29T15:00:00Z","reason":{"code":"adapter.hearth-adapter-homeassistant.connection_lost"}}}
	}`)
	external := heartbeat["data"].(map[string]any)["external_system"].(map[string]any)
	if err := schemas[contractsv1.AdapterHeartbeatRequestSchemaID].Validate(heartbeat); err != nil {
		t.Fatal(err)
	}
	delete(external, "reason")
	if err := schemas[contractsv1.AdapterHeartbeatRequestSchemaID].Validate(heartbeat); err == nil {
		t.Fatal("unhealthy heartbeat without a reason unexpectedly accepted")
	}
	external["status"] = "healthy"
	if err := schemas[contractsv1.AdapterHeartbeatRequestSchemaID].Validate(heartbeat); err != nil {
		t.Fatalf("healthy heartbeat without a reason rejected: %v", err)
	}
	external["reason"] = map[string]any{"code": "hearth.network_unreachable"}
	if err := schemas[contractsv1.AdapterHeartbeatRequestSchemaID].Validate(heartbeat); err == nil {
		t.Fatal("healthy heartbeat with a reason unexpectedly accepted")
	}
	external["status"] = "unhealthy"
	external["reason"] = map[string]any{"code": "Hearth.NetworkUnreachable"}
	if err := schemas[contractsv1.AdapterHeartbeatRequestSchemaID].Validate(heartbeat); err == nil {
		t.Fatal("non-lowercase reason code unexpectedly accepted")
	}
}

func TestEntityAvailabilitySchemaEnforcesBatchAndReasonBounds(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.EntityAvailabilityRequestSchemaID]
	request := decodeObject(t, availabilityRequestFixture())
	data := request["data"].(map[string]any)
	original := data["entities"].([]any)[0]

	for _, count := range []int{0, 1, 256, 257} {
		entities := make([]any, count)
		for index := range entities {
			entities[index] = map[string]any{
				"entity_id":          fmt.Sprintf("ent_01890f47-7a6b-7c4d-8e9f-%012x", index+1),
				"status":             "available",
				"source_observed_at": "2026-08-29T15:00:00Z",
			}
		}
		data["entities"] = entities
		err := schema.Validate(request)
		valid := count >= 1 && count <= 256
		if valid && err != nil {
			t.Fatalf("%d Entity reports rejected: %v", count, err)
		}
		if !valid && err == nil {
			t.Fatalf("%d Entity reports unexpectedly accepted", count)
		}
	}

	data["entities"] = []any{original, original}
	if err := schema.Validate(request); err == nil {
		t.Fatal("identical duplicate Entity reports unexpectedly accepted")
	}

	report := original.(map[string]any)
	data["entities"] = []any{report}
	report["status"] = "available"
	delete(report, "reason")
	if err := schema.Validate(request); err != nil {
		t.Fatalf("available report without a reason rejected: %v", err)
	}
	report["reason"] = map[string]any{"code": "hearth.entity_unavailable"}
	if err := schema.Validate(request); err == nil {
		t.Fatal("available report with a reason unexpectedly accepted")
	}
	report["status"] = "unavailable"
	if err := schema.Validate(request); err != nil {
		t.Fatalf("unavailable report with a reason rejected: %v", err)
	}
	delete(report, "reason")
	if err := schema.Validate(request); err == nil {
		t.Fatal("unavailable report without a reason unexpectedly accepted")
	}
}

func TestClaimAndTypedRejectionBranches(t *testing.T) {
	t.Parallel()
	schemas := compileSchemas(t)
	claim := decodeObject(t, `{
		"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:adapter-claim-response:v1",
		"emitted_at":"2026-08-29T15:00:01Z",
		"correlation_id":"`+testCorrelationID+`",
		"causation_id":"clm_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"data":{"status":"rejected","error":{"code":"adapter_active","message":"Adapter already active","retry_after":"2026-08-29T15:00:16Z"}}
	}`)
	if err := schemas[contractsv1.AdapterClaimResponseSchemaID].Validate(claim); err != nil {
		t.Fatal(err)
	}
	errorData := claim["data"].(map[string]any)["error"].(map[string]any)
	delete(errorData, "retry_after")
	if err := schemas[contractsv1.AdapterClaimResponseSchemaID].Validate(claim); err == nil {
		t.Fatal("adapter_active rejection without retry_after unexpectedly accepted")
	}
	errorData["code"] = "adapter_archived"
	if err := schemas[contractsv1.AdapterClaimResponseSchemaID].Validate(claim); err != nil {
		t.Fatalf("adapter_archived rejection rejected: %v", err)
	}

	for _, test := range []struct {
		name     string
		schemaID string
		fixture  string
	}{
		{
			"heartbeat fenced", contractsv1.AdapterHeartbeatResponseSchemaID,
			`{"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:adapter-heartbeat-response:v1","emitted_at":"2026-08-29T15:00:01Z","correlation_id":"` + testCorrelationID + `","causation_id":"hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"status":"rejected","error":{"code":"runtime_fenced","message":"runtime fenced"}}}`,
		},
		{
			"release fenced", contractsv1.AdapterReleaseResponseSchemaID,
			`{"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:adapter-release-response:v1","emitted_at":"2026-08-29T15:00:01Z","correlation_id":"` + testCorrelationID + `","causation_id":"rel_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"status":"rejected","error":{"code":"runtime_fenced","message":"runtime fenced"}}}`,
		},
		{
			"availability fenced", contractsv1.EntityAvailabilityResponseSchemaID,
			`{"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:entity-availability-response:v1","emitted_at":"2026-08-29T15:00:01Z","correlation_id":"` + testCorrelationID + `","causation_id":"avl_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"status":"rejected","error":{"code":"runtime_fenced","message":"runtime fenced"}}}`,
		},
		{
			"availability unknown Entity", contractsv1.EntityAvailabilityResponseSchemaID,
			`{"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:entity-availability-response:v1","emitted_at":"2026-08-29T15:00:01Z","correlation_id":"` + testCorrelationID + `","causation_id":"avl_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"status":"rejected","error":{"code":"unknown_entity","message":"Entity not found","entity_id":"` + testEntityID + `"}}}`,
		},
		{
			"registration fenced", contractsv1.RegistrationResponseSchemaID,
			`{"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:registration-response:v1","emitted_at":"2026-08-29T15:00:01Z","correlation_id":"` + testCorrelationID + `","causation_id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"status":"rejected","error":{"code":"runtime_fenced","message":"runtime fenced"}}}`,
		},
		{
			"enablement fenced", contractsv1.EntityEnablementResponseSchemaID,
			`{"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:entity-enablement-response:v1","emitted_at":"2026-08-29T15:00:01Z","correlation_id":"` + testCorrelationID + `","causation_id":"ena_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"status":"rejected","error":{"code":"runtime_fenced","message":"runtime fenced"}}}`,
		},
		{
			"Entity unavailable", contractsv1.CommandResponseSchemaID,
			`{"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:command-response:v1","emitted_at":"2026-08-29T15:00:01Z","correlation_id":"` + testCorrelationID + `","causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","status":"rejected","error":{"code":"entity_unavailable","message":"Entity unavailable"}}}`,
		},
	} {
		if err := schemas[test.schemaID].Validate(decodeObject(t, test.fixture)); err != nil {
			t.Errorf("%s: %v", test.name, err)
		}
	}
}

func TestExistingPayloadsStillTakeRuntimeIdentityOnlyFromSubject(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.RegistrationRequestSchemaID]
	request := decodeObject(t, `{
		"id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:registration-request:v1",
		"emitted_at":"2026-08-29T15:00:01Z",
		"correlation_id":"`+testCorrelationID+`",
		"data":{"binding_key":"office-light","device":{"name":"Office Light","kind":"light"},"entities":[{"key":"power","external_id":"light.office","name":"Power","type":"hearth.power/v1","support":{"state":{},"operations":{"set":{}}}}]}
	}`)
	if err := schema.Validate(request); err != nil {
		t.Fatal(err)
	}
	request["data"].(map[string]any)["runtime_id"] = testRuntimeID
	if err := schema.Validate(request); err == nil {
		t.Fatal("registration payload runtime_id unexpectedly accepted")
	}
}

func availabilityRequestFixture() string {
	return `{
		"id":"avl_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:entity-availability-request:v1",
		"emitted_at":"2026-08-29T15:00:01Z",
		"correlation_id":"` + testCorrelationID + `",
		"data":{"entities":[{"entity_id":"` + testEntityID + `","status":"unavailable","source_observed_at":"2026-08-29T15:00:00Z","reason":{"code":"hearth.entity_unavailable","detail":"resource unavailable"}}]}
	}`
}

func decodeObject(t *testing.T, fixture string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(fixture), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
