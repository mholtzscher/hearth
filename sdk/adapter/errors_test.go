package adapter //nolint:testpackage // Tests pin package-private registration rejection codes to the wire contract.

import (
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

// TestRegistrationImmutableSupportChangeMatchesWireContract pins the SDK
// rejection code to the authoritative registration-response schema, so a rename
// on either side of the NATS boundary fails here rather than at runtime.
func TestRegistrationImmutableSupportChangeMatchesWireContract(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	payload := `{
		"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:registration-response:v1",
		"emitted_at":"2026-08-20T12:34:56Z",
		"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"causation_id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"data":{"status":"rejected","error":{"code":"` + string(RegistrationImmutableSupportChange) + `","message":"an existing entity cannot change immutable support"}}
	}`
	if validateErr := validator.Validate(
		contractsv1.RegistrationResponseSchemaID,
		[]byte(payload),
	); validateErr != nil {
		t.Fatalf("registration-response schema rejected %q: %v", RegistrationImmutableSupportChange, validateErr)
	}
}
