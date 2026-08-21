package v1

import "testing"

func TestValidatorRejectsTrailingJSON(t *testing.T) {
	validator, err := Compile()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{
		"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:observation:v1",
		"emitted_at":"2026-08-20T12:34:56Z",
		"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value":true,"adapter_received_at":"2026-08-20T12:34:56Z"}
	} false`)
	if err := validator.Validate(ObservationSchemaID, payload); err == nil {
		t.Fatal("trailing JSON value unexpectedly accepted")
	}
}
