package v1_test

import (
	"sync"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

func TestConcurrentCompileAndValidation(t *testing.T) {
	t.Parallel()
	valid := []byte(
		`{"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:observation:v1","emitted_at":"2026-08-20T12:34:56Z","correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value":true,"adapter_received_at":"2026-08-20T12:34:56Z"}}`,
	)
	first, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	second, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	*first = contractsv1.Validator{}
	if validationErr := second.Validate(contractsv1.ObservationSchemaID, valid); validationErr != nil {
		t.Fatal(validationErr)
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			validator, compileErr := contractsv1.Compile()
			if compileErr != nil {
				t.Error(compileErr)
				return
			}
			if validationErr := validator.Validate(contractsv1.ObservationSchemaID, valid); validationErr != nil {
				t.Error(validationErr)
			}
			if validationErr := validator.Validate(
				contractsv1.ObservationSchemaID,
				[]byte(`{}`),
			); validationErr == nil {
				t.Error("invalid observation accepted")
			}
		})
	}
	workers.Wait()
}

func TestValidatorRejectsTrailingJSON(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
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
	if validationErr := validator.Validate(contractsv1.ObservationSchemaID, payload); validationErr == nil {
		t.Fatal("trailing JSON value unexpectedly accepted")
	}
}
