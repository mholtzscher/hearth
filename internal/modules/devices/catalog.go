package devices

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type OutcomePolicy string

const OutcomeParameterEqualsState OutcomePolicy = "parameter_equals_state"

type OperationDefinition struct {
	ParametersSchema json.RawMessage
	OutcomePolicy    OutcomePolicy
	OutcomeParameter string
	Deadline         time.Duration
}

type EntityTypeDefinition struct {
	ID                   EntityTypeID
	StateSchema          json.RawMessage
	ConstraintsSchema    json.RawMessage
	OperationDefinitions map[OperationName]OperationDefinition
}

type ResolvedCommand struct {
	Parameters CommandParameters
	Deadline   time.Duration
}

type compiledOperationDefinition struct {
	definition OperationDefinition
	parameters *jsonschema.Schema
}

type compiledType struct {
	state                *jsonschema.Schema
	constraints          *jsonschema.Schema
	operationDefinitions map[OperationName]compiledOperationDefinition
}

type TypeCatalog struct {
	types map[EntityTypeID]compiledType
}

func NewTypeCatalog(definitions []EntityTypeDefinition) (*TypeCatalog, error) {
	catalog := &TypeCatalog{types: make(map[EntityTypeID]compiledType, len(definitions))}
	for _, definition := range definitions {
		if definition.ID == "" {
			return nil, fmt.Errorf("entity type ID is required")
		}
		if _, exists := catalog.types[definition.ID]; exists {
			return nil, fmt.Errorf("duplicate entity type %q", definition.ID)
		}

		state, err := compileSchema(definition.ID, "state", definition.StateSchema)
		if err != nil {
			return nil, err
		}
		constraints, err := compileSchema(definition.ID, "constraints", definition.ConstraintsSchema)
		if err != nil {
			return nil, err
		}

		compiled := compiledType{
			state:                state,
			constraints:          constraints,
			operationDefinitions: make(map[OperationName]compiledOperationDefinition, len(definition.OperationDefinitions)),
		}
		for operationName, operationDefinition := range definition.OperationDefinitions {
			if operationName == "" {
				return nil, fmt.Errorf("entity type %q has an operation without a name", definition.ID)
			}
			if operationDefinition.OutcomePolicy != OutcomeParameterEqualsState {
				return nil, fmt.Errorf("entity type %q operation %q has unsupported outcome policy %q", definition.ID, operationName, operationDefinition.OutcomePolicy)
			}
			if operationDefinition.OutcomeParameter == "" {
				return nil, fmt.Errorf("entity type %q operation %q has no outcome parameter", definition.ID, operationName)
			}
			if operationDefinition.Deadline <= 0 {
				return nil, fmt.Errorf("entity type %q operation %q has a non-positive deadline", definition.ID, operationName)
			}
			parameters, err := compileSchema(definition.ID, string(operationName)+" parameters", operationDefinition.ParametersSchema)
			if err != nil {
				return nil, err
			}
			compiled.operationDefinitions[operationName] = compiledOperationDefinition{definition: operationDefinition, parameters: parameters}
		}
		catalog.types[definition.ID] = compiled
	}
	return catalog, nil
}

func NewFirstLightTypeCatalog() (*TypeCatalog, error) {
	return NewTypeCatalog([]EntityTypeDefinition{{
		ID:                EntityTypePowerV1,
		StateSchema:       json.RawMessage(`{"type":"boolean"}`),
		ConstraintsSchema: json.RawMessage(`{"type":"object","maxProperties":0,"additionalProperties":false}`),
		OperationDefinitions: map[OperationName]OperationDefinition{
			OperationNameSet: {
				ParametersSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"boolean"}},"required":["value"],"additionalProperties":false}`),
				OutcomePolicy:    OutcomeParameterEqualsState,
				OutcomeParameter: "value",
				Deadline:         10 * time.Second,
			},
		},
	}})
}

func (catalog *TypeCatalog) ValidateEntity(typeID EntityTypeID, constraints json.RawMessage, supportedOperations []OperationName) error {
	definition, err := catalog.resolve(typeID)
	if err != nil {
		return err
	}
	value, _, err := decodeJSON(constraints)
	if err != nil {
		return fmt.Errorf("invalid constraints for entity type %q: %w", typeID, err)
	}
	if err := definition.constraints.Validate(value); err != nil {
		return fmt.Errorf("invalid constraints for entity type %q: %w", typeID, err)
	}
	if len(supportedOperations) == 0 {
		return fmt.Errorf("entity type %q requires at least one operation", typeID)
	}
	seen := make(map[OperationName]struct{}, len(supportedOperations))
	for _, operationName := range supportedOperations {
		if _, duplicate := seen[operationName]; duplicate {
			return fmt.Errorf("entity type %q has duplicate supported operation name %q", typeID, operationName)
		}
		seen[operationName] = struct{}{}
		if _, allowed := definition.operationDefinitions[operationName]; !allowed {
			return fmt.Errorf("entity type %q does not allow operation %q", typeID, operationName)
		}
	}
	if typeID == EntityTypePowerV1 && (len(supportedOperations) != 1 || supportedOperations[0] != OperationNameSet) {
		return fmt.Errorf("entity type %q requires exactly operation %q", typeID, OperationNameSet)
	}
	return nil
}

func (catalog *TypeCatalog) NormalizeState(entity Entity, value Value) (Value, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return nil, err
	}
	decoded, normalized, err := decodeJSON(json.RawMessage(value))
	if err != nil {
		return nil, fmt.Errorf("invalid state for entity type %q: %w", entity.TypeID, err)
	}
	if err := definition.state.Validate(decoded); err != nil {
		return nil, fmt.Errorf("invalid state for entity type %q: %w", entity.TypeID, err)
	}
	return Value(normalized), nil
}

func (catalog *TypeCatalog) EqualState(entity Entity, left, right Value) (bool, error) {
	normalizedLeft, err := catalog.NormalizeState(entity, left)
	if err != nil {
		return false, err
	}
	normalizedRight, err := catalog.NormalizeState(entity, right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(normalizedLeft, normalizedRight), nil
}

func (catalog *TypeCatalog) ResolveCommand(entity Entity, operationName OperationName, parameters CommandParameters) (ResolvedCommand, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return ResolvedCommand{}, err
	}
	if !supportsOperationName(entity.SupportedOperations, operationName) {
		return ResolvedCommand{}, fmt.Errorf("operation %q is not supported by entity %q", operationName, entity.ID)
	}
	compiled, exists := definition.operationDefinitions[operationName]
	if !exists {
		return ResolvedCommand{}, fmt.Errorf("entity type %q does not define operation %q", entity.TypeID, operationName)
	}
	decoded, normalized, err := decodeJSON(json.RawMessage(parameters))
	if err != nil {
		return ResolvedCommand{}, fmt.Errorf("invalid parameters for operation %q: %w", operationName, err)
	}
	if err := compiled.parameters.Validate(decoded); err != nil {
		return ResolvedCommand{}, fmt.Errorf("invalid parameters for operation %q: %w", operationName, err)
	}
	return ResolvedCommand{Parameters: CommandParameters(normalized), Deadline: compiled.definition.Deadline}, nil
}

func (catalog *TypeCatalog) Satisfies(entity Entity, command CommandRecord, value Value) (bool, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return false, err
	}
	operationDefinition, exists := definition.operationDefinitions[command.OperationName]
	if !exists {
		return false, fmt.Errorf("entity type %q does not define operation %q", entity.TypeID, command.OperationName)
	}
	decoded, normalized, err := decodeJSON(json.RawMessage(command.Parameters))
	if err != nil {
		return false, fmt.Errorf("invalid parameters for operation %q: %w", command.OperationName, err)
	}
	if err := operationDefinition.parameters.Validate(decoded); err != nil {
		return false, fmt.Errorf("invalid parameters for operation %q: %w", command.OperationName, err)
	}
	var parameters map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &parameters); err != nil {
		return false, fmt.Errorf("decode normalized parameters: %w", err)
	}
	outcome, exists := parameters[operationDefinition.definition.OutcomeParameter]
	if !exists {
		return false, fmt.Errorf("operation %q outcome parameter %q is absent", command.OperationName, operationDefinition.definition.OutcomeParameter)
	}
	return catalog.EqualState(entity, Value(outcome), value)
}

func (catalog *TypeCatalog) resolve(typeID EntityTypeID) (compiledType, error) {
	if catalog == nil {
		return compiledType{}, fmt.Errorf("entity type catalog is nil")
	}
	definition, exists := catalog.types[typeID]
	if !exists {
		return compiledType{}, fmt.Errorf("unknown entity type %q", typeID)
	}
	return definition, nil
}

func compileSchema(typeID EntityTypeID, name string, raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("entity type %q has no %s schema", typeID, name)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("compile entity type %q %s schema: %w", typeID, name, err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "schema.json"
	if err := compiler.AddResource(location, document); err != nil {
		return nil, fmt.Errorf("compile entity type %q %s schema: %w", typeID, name, err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("compile entity type %q %s schema: %w", typeID, name, err)
	}
	return schema, nil
}

func decodeJSON(raw json.RawMessage) (any, []byte, error) {
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("JSON value is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, nil, fmt.Errorf("multiple JSON values")
		}
		return nil, nil, err
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	return value, normalized, nil
}

func supportsOperationName(supportedOperations []OperationName, target OperationName) bool {
	for _, operationName := range supportedOperations {
		if operationName == target {
			return true
		}
	}
	return false
}
