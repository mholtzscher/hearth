package devices

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFirstLightCatalog(t *testing.T) {
	catalog, err := NewFirstLightTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	entity := Entity{
		ID:                  EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"),
		TypeID:              EntityTypePowerV1,
		Constraints:         json.RawMessage(`{}`),
		SupportedOperations: []OperationName{OperationNameSet},
	}
	if err := catalog.ValidateEntity(entity.TypeID, entity.Constraints, entity.SupportedOperations); err != nil {
		t.Fatalf("validate first-light entity: %v", err)
	}

	state, err := catalog.NormalizeState(entity, Value(` true `))
	if err != nil {
		t.Fatal(err)
	}
	if string(state) != "true" {
		t.Fatalf("normalized state = %s", state)
	}
	if _, err := catalog.NormalizeState(entity, Value(`"on"`)); err == nil {
		t.Fatal("non-boolean state unexpectedly accepted")
	}

	command, err := catalog.ResolveCommand(entity, OperationNameSet, CommandParameters(`{ "value": true }`))
	if err != nil {
		t.Fatal(err)
	}
	if string(command.Parameters) != `{"value":true}` {
		t.Fatalf("normalized parameters = %s", command.Parameters)
	}
	if command.Deadline != 10*time.Second {
		t.Fatalf("deadline = %s", command.Deadline)
	}
	if _, err := catalog.ResolveCommand(entity, OperationNameSet, CommandParameters(`{"value":true,"transition":1}`)); err == nil {
		t.Fatal("additional parameter unexpectedly accepted")
	}

	record := CommandRecord{OperationName: OperationNameSet, Parameters: command.Parameters}
	satisfied, err := catalog.Satisfies(entity, record, Value(`true`))
	if err != nil || !satisfied {
		t.Fatalf("matching outcome: satisfied=%v err=%v", satisfied, err)
	}
	satisfied, err = catalog.Satisfies(entity, record, Value(`false`))
	if err != nil || satisfied {
		t.Fatalf("nonmatching outcome: satisfied=%v err=%v", satisfied, err)
	}

	entity.SupportedOperations = nil
	satisfied, err = catalog.Satisfies(entity, record, Value(`true`))
	if err != nil || !satisfied {
		t.Fatalf("recorded command after descriptor update: satisfied=%v err=%v", satisfied, err)
	}
}

func TestFirstLightCatalogRejectsInvalidDescriptors(t *testing.T) {
	catalog, err := NewFirstLightTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name                string
		typeID              EntityTypeID
		constraints         json.RawMessage
		supportedOperations []OperationName
	}{
		{"unknown type", "vendor.power/v1", json.RawMessage(`{}`), []OperationName{OperationNameSet}},
		{"constraints", EntityTypePowerV1, json.RawMessage(`{"minimum":1}`), []OperationName{OperationNameSet}},
		{"missing operation", EntityTypePowerV1, json.RawMessage(`{}`), nil},
		{"unknown operation", EntityTypePowerV1, json.RawMessage(`{}`), []OperationName{"toggle"}},
		{"duplicate operation", EntityTypePowerV1, json.RawMessage(`{}`), []OperationName{OperationNameSet, OperationNameSet}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := catalog.ValidateEntity(test.typeID, test.constraints, test.supportedOperations); err == nil {
				t.Fatal("descriptor unexpectedly accepted")
			}
		})
	}
}

func TestCatalogRejectsInvalidDefinitions(t *testing.T) {
	validOperation := OperationDefinition{
		ParametersSchema: json.RawMessage(`{"type":"object"}`),
		OutcomePolicy:    OutcomeParameterEqualsState,
		OutcomeParameter: "value",
		Deadline:         time.Second,
	}
	valid := EntityTypeDefinition{
		ID:                   "test.value/v1",
		StateSchema:          json.RawMessage(`{"type":"boolean"}`),
		ConstraintsSchema:    json.RawMessage(`{"type":"object"}`),
		OperationDefinitions: map[OperationName]OperationDefinition{OperationNameSet: validOperation},
	}

	tests := []struct {
		name        string
		definitions []EntityTypeDefinition
	}{
		{"duplicate type", []EntityTypeDefinition{valid, valid}},
		{"malformed schema", []EntityTypeDefinition{{ID: "test.bad/v1", StateSchema: json.RawMessage(`{`), ConstraintsSchema: valid.ConstraintsSchema}}},
		{"unsupported policy", []EntityTypeDefinition{definitionWithOperation(valid, func(operation *OperationDefinition) { operation.OutcomePolicy = "custom" })}},
		{"missing outcome parameter", []EntityTypeDefinition{definitionWithOperation(valid, func(operation *OperationDefinition) { operation.OutcomeParameter = "" })}},
		{"non-positive deadline", []EntityTypeDefinition{definitionWithOperation(valid, func(operation *OperationDefinition) { operation.Deadline = 0 })}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTypeCatalog(test.definitions); err == nil {
				t.Fatal("definition unexpectedly accepted")
			}
		})
	}
}

func TestCatalogDefensivelyCopiesInputsAndNormalizesObjects(t *testing.T) {
	stateSchema := json.RawMessage(`{"type":"object","required":["a","b"],"additionalProperties":false,"properties":{"a":{"type":"boolean"},"b":{"type":"boolean"}}}`)
	definition := EntityTypeDefinition{
		ID:                "test.object/v1",
		StateSchema:       stateSchema,
		ConstraintsSchema: json.RawMessage(`{"type":"object"}`),
		OperationDefinitions: map[OperationName]OperationDefinition{
			OperationNameSet: {
				ParametersSchema: json.RawMessage(`{"type":"object","required":["value"],"properties":{"value":{"type":"object"}}}`),
				OutcomePolicy:    OutcomeParameterEqualsState, OutcomeParameter: "value", Deadline: time.Second,
			},
		},
	}
	catalog, err := NewTypeCatalog([]EntityTypeDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	stateSchema[0] = '['
	delete(definition.OperationDefinitions, OperationNameSet)

	entity := Entity{TypeID: "test.object/v1", SupportedOperations: []OperationName{OperationNameSet}}
	equal, err := catalog.EqualState(entity, Value(`{"b":false,"a":true}`), Value(`{"a":true,"b":false}`))
	if err != nil || !equal {
		t.Fatalf("semantic object equality: equal=%v err=%v", equal, err)
	}
	if _, err := catalog.ResolveCommand(entity, OperationNameSet, CommandParameters(`{"value":{}}`)); err != nil {
		t.Fatalf("catalog changed after caller mutation: %v", err)
	}
}

func definitionWithOperation(base EntityTypeDefinition, mutate func(*OperationDefinition)) EntityTypeDefinition {
	copy := base
	operationDefinition := base.OperationDefinitions[OperationNameSet]
	mutate(&operationDefinition)
	copy.OperationDefinitions = map[OperationName]OperationDefinition{OperationNameSet: operationDefinition}
	return copy
}
