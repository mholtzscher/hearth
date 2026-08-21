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
	Name             Operation
	ParametersSchema json.RawMessage
	OutcomePolicy    OutcomePolicy
	OutcomeParameter string
	Deadline         time.Duration
}

type EntityTypeDefinition struct {
	ID                EntityTypeID
	StateSchema       json.RawMessage
	ConstraintsSchema json.RawMessage
	Operations        map[Operation]OperationDefinition
}

type ResolvedCommand struct {
	Parameters CommandParameters
	Deadline   time.Duration
}

type compiledOperation struct {
	definition OperationDefinition
	parameters *jsonschema.Schema
}

type compiledType struct {
	state       *jsonschema.Schema
	constraints *jsonschema.Schema
	operations  map[Operation]compiledOperation
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
			state:       state,
			constraints: constraints,
			operations:  make(map[Operation]compiledOperation, len(definition.Operations)),
		}
		operationNames := make(map[Operation]struct{}, len(definition.Operations))
		for key, operation := range definition.Operations {
			if key == "" || operation.Name == "" {
				return nil, fmt.Errorf("entity type %q has an operation without a name", definition.ID)
			}
			if key != operation.Name {
				return nil, fmt.Errorf("entity type %q operation key %q does not match name %q", definition.ID, key, operation.Name)
			}
			if _, exists := operationNames[operation.Name]; exists {
				return nil, fmt.Errorf("entity type %q has duplicate operation %q", definition.ID, operation.Name)
			}
			operationNames[operation.Name] = struct{}{}
			if operation.OutcomePolicy != OutcomeParameterEqualsState {
				return nil, fmt.Errorf("entity type %q operation %q has unsupported outcome policy %q", definition.ID, operation.Name, operation.OutcomePolicy)
			}
			if operation.OutcomeParameter == "" {
				return nil, fmt.Errorf("entity type %q operation %q has no outcome parameter", definition.ID, operation.Name)
			}
			if operation.Deadline <= 0 {
				return nil, fmt.Errorf("entity type %q operation %q has a non-positive deadline", definition.ID, operation.Name)
			}
			parameters, err := compileSchema(definition.ID, string(operation.Name)+" parameters", operation.ParametersSchema)
			if err != nil {
				return nil, err
			}
			compiled.operations[key] = compiledOperation{definition: operation, parameters: parameters}
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
		Operations: map[Operation]OperationDefinition{
			OperationSet: {
				Name:             OperationSet,
				ParametersSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"boolean"}},"required":["value"],"additionalProperties":false}`),
				OutcomePolicy:    OutcomeParameterEqualsState,
				OutcomeParameter: "value",
				Deadline:         10 * time.Second,
			},
		},
	}})
}

func (catalog *TypeCatalog) ValidateEntity(typeID EntityTypeID, constraints json.RawMessage, operations []Operation) error {
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
	if len(operations) == 0 {
		return fmt.Errorf("entity type %q requires at least one operation", typeID)
	}
	seen := make(map[Operation]struct{}, len(operations))
	for _, operation := range operations {
		if _, duplicate := seen[operation]; duplicate {
			return fmt.Errorf("entity type %q has duplicate operation %q", typeID, operation)
		}
		seen[operation] = struct{}{}
		if _, allowed := definition.operations[operation]; !allowed {
			return fmt.Errorf("entity type %q does not allow operation %q", typeID, operation)
		}
	}
	if typeID == EntityTypePowerV1 && (len(operations) != 1 || operations[0] != OperationSet) {
		return fmt.Errorf("entity type %q requires exactly operation %q", typeID, OperationSet)
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

func (catalog *TypeCatalog) ResolveCommand(entity Entity, operation Operation, parameters CommandParameters) (ResolvedCommand, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return ResolvedCommand{}, err
	}
	if !containsOperation(entity.Operations, operation) {
		return ResolvedCommand{}, fmt.Errorf("operation %q is unavailable for entity %q", operation, entity.ID)
	}
	compiled, exists := definition.operations[operation]
	if !exists {
		return ResolvedCommand{}, fmt.Errorf("entity type %q does not define operation %q", entity.TypeID, operation)
	}
	decoded, normalized, err := decodeJSON(json.RawMessage(parameters))
	if err != nil {
		return ResolvedCommand{}, fmt.Errorf("invalid parameters for operation %q: %w", operation, err)
	}
	if err := compiled.parameters.Validate(decoded); err != nil {
		return ResolvedCommand{}, fmt.Errorf("invalid parameters for operation %q: %w", operation, err)
	}
	return ResolvedCommand{Parameters: CommandParameters(normalized), Deadline: compiled.definition.Deadline}, nil
}

func (catalog *TypeCatalog) Satisfies(entity Entity, command CommandRecord, value Value) (bool, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return false, err
	}
	operation, exists := definition.operations[command.Operation]
	if !exists {
		return false, fmt.Errorf("entity type %q does not define operation %q", entity.TypeID, command.Operation)
	}
	decoded, normalized, err := decodeJSON(json.RawMessage(command.Parameters))
	if err != nil {
		return false, fmt.Errorf("invalid parameters for operation %q: %w", command.Operation, err)
	}
	if err := operation.parameters.Validate(decoded); err != nil {
		return false, fmt.Errorf("invalid parameters for operation %q: %w", command.Operation, err)
	}
	var parameters map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &parameters); err != nil {
		return false, fmt.Errorf("decode normalized parameters: %w", err)
	}
	outcome, exists := parameters[operation.definition.OutcomeParameter]
	if !exists {
		return false, fmt.Errorf("operation %q outcome parameter %q is absent", command.Operation, operation.definition.OutcomeParameter)
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

func containsOperation(operations []Operation, target Operation) bool {
	for _, operation := range operations {
		if operation == target {
			return true
		}
	}
	return false
}
