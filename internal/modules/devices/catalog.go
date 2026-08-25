package devices

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/mholtzscher/hearth/entitytypes"
)

var operationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type operationDefinition[State, Support any] struct {
	name  OperationName
	build func(*entitytypes.JSONCodec[State], *entitytypes.JSONCodec[Support]) erasedOperationDefinition
	err   error
}

func defineOperation[State, Support, OperationSupport, Parameters any](
	name OperationName,
	parameters *entitytypes.JSONCodec[Parameters],
	selectSupport func(Support) (OperationSupport, bool),
	validateParameters func(Support, OperationSupport, Parameters) error,
	deadline time.Duration,
	satisfies func(Parameters, State) bool,
) operationDefinition[State, Support] {
	definition := operationDefinition[State, Support]{name: name}
	switch {
	case !operationNamePattern.MatchString(string(name)):
		definition.err = fmt.Errorf("operation name %q is not subject-safe", name)
	case parameters == nil:
		definition.err = fmt.Errorf("operation %q has no parameter codec", name)
	case selectSupport == nil:
		definition.err = fmt.Errorf("operation %q has no support selector", name)
	case validateParameters == nil:
		definition.err = fmt.Errorf("operation %q has no parameter validator", name)
	case deadline <= 0:
		definition.err = fmt.Errorf("operation %q has a non-positive deadline", name)
	case satisfies == nil:
		definition.err = fmt.Errorf("operation %q has no outcome matcher", name)
	default:
		definition.build = func(state *entitytypes.JSONCodec[State], support *entitytypes.JSONCodec[Support]) erasedOperationDefinition {
			return erasedOperationDefinition{
				resolve: func(rawSupport EntitySupport, rawParameters CommandParameters) (CommandParameters, error) {
					typedSupport, _, err := support.Decode(json.RawMessage(rawSupport))
					if err != nil {
						return nil, fmt.Errorf("decode entity support: %w", err)
					}
					operationSupport, supported := selectSupport(typedSupport)
					if !supported {
						return nil, fmt.Errorf("operation %q is not supported", name)
					}
					typedParameters, normalized, err := parameters.Decode(json.RawMessage(rawParameters))
					if err != nil {
						return nil, fmt.Errorf("decode parameters for operation %q: %w", name, err)
					}
					if err := validateParameters(typedSupport, operationSupport, typedParameters); err != nil {
						return nil, fmt.Errorf("validate parameters for operation %q: %w", name, err)
					}
					return CommandParameters(normalized), nil
				},
				deadline: deadline,
				satisfies: func(rawParameters CommandParameters, rawState Value) (bool, error) {
					typedParameters, _, err := parameters.Decode(json.RawMessage(rawParameters))
					if err != nil {
						return false, fmt.Errorf("decode recorded parameters for operation %q: %w", name, err)
					}
					typedState, _, err := state.Decode(json.RawMessage(rawState))
					if err != nil {
						return false, fmt.Errorf("decode state for operation %q: %w", name, err)
					}
					return satisfies(typedParameters, typedState), nil
				},
			}
		}
	}
	return definition
}

type entityTypeDefinition struct {
	id               EntityTypeID
	normalizeSupport func(EntitySupport) (EntitySupport, error)
	normalizeState   func(EntitySupport, Value) (Value, error)
	equalState       func(EntitySupport, Value, Value) (bool, error)
	operations       map[OperationName]erasedOperationDefinition
}

type erasedOperationDefinition struct {
	resolve   func(EntitySupport, CommandParameters) (CommandParameters, error)
	deadline  time.Duration
	satisfies func(CommandParameters, Value) (bool, error)
}

func defineEntityType[State, Support any](
	id EntityTypeID,
	state *entitytypes.JSONCodec[State],
	support *entitytypes.JSONCodec[Support],
	validateSupportedState func(Support, State) error,
	equalState func(State, State) bool,
	operations ...operationDefinition[State, Support],
) (entityTypeDefinition, error) {
	if id == "" {
		return entityTypeDefinition{}, fmt.Errorf("entity type ID is required")
	}
	if state == nil {
		return entityTypeDefinition{}, fmt.Errorf("entity type %q has no state codec", id)
	}
	if support == nil {
		return entityTypeDefinition{}, fmt.Errorf("entity type %q has no support codec", id)
	}
	if validateSupportedState == nil {
		return entityTypeDefinition{}, fmt.Errorf("entity type %q has no supported-state validator", id)
	}
	if equalState == nil {
		return entityTypeDefinition{}, fmt.Errorf("entity type %q has no state equality function", id)
	}

	definition := entityTypeDefinition{
		id:         id,
		operations: make(map[OperationName]erasedOperationDefinition, len(operations)),
	}
	for _, operation := range operations {
		if operation.err != nil {
			return entityTypeDefinition{}, fmt.Errorf("define entity type %q: %w", id, operation.err)
		}
		if operation.build == nil {
			return entityTypeDefinition{}, fmt.Errorf("entity type %q has an invalid operation %q", id, operation.name)
		}
		if _, duplicate := definition.operations[operation.name]; duplicate {
			return entityTypeDefinition{}, fmt.Errorf("entity type %q has duplicate operation %q", id, operation.name)
		}
		definition.operations[operation.name] = operation.build(state, support)
	}

	definition.normalizeSupport = func(raw EntitySupport) (EntitySupport, error) {
		_, normalized, err := support.Decode(json.RawMessage(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid support for entity type %q: %w", id, err)
		}
		return EntitySupport(normalized), nil
	}
	definition.normalizeState = func(rawSupport EntitySupport, rawState Value) (Value, error) {
		typedSupport, _, err := support.Decode(json.RawMessage(rawSupport))
		if err != nil {
			return nil, fmt.Errorf("invalid support for entity type %q: %w", id, err)
		}
		typedState, normalized, err := state.Decode(json.RawMessage(rawState))
		if err != nil {
			return nil, fmt.Errorf("invalid state for entity type %q: %w", id, err)
		}
		if err := validateSupportedState(typedSupport, typedState); err != nil {
			return nil, fmt.Errorf("state is unsupported by entity type %q: %w", id, err)
		}
		return Value(normalized), nil
	}
	definition.equalState = func(rawSupport EntitySupport, persisted, incoming Value) (bool, error) {
		typedSupport, _, err := support.Decode(json.RawMessage(rawSupport))
		if err != nil {
			return false, fmt.Errorf("invalid support for entity type %q: %w", id, err)
		}
		typedPersisted, _, err := state.Decode(json.RawMessage(persisted))
		if err != nil {
			return false, fmt.Errorf("invalid persisted state for entity type %q: %w", id, err)
		}
		typedIncoming, _, err := state.Decode(json.RawMessage(incoming))
		if err != nil {
			return false, fmt.Errorf("invalid incoming state for entity type %q: %w", id, err)
		}
		if err := validateSupportedState(typedSupport, typedIncoming); err != nil {
			return false, fmt.Errorf("incoming state is unsupported by entity type %q: %w", id, err)
		}
		return equalState(typedPersisted, typedIncoming), nil
	}
	return definition, nil
}

type resolvedCommand struct {
	parameters CommandParameters
	deadline   time.Duration
}

type typeCatalog struct {
	types map[EntityTypeID]entityTypeDefinition
}

func newTypeCatalog(definitions []entityTypeDefinition) (*typeCatalog, error) {
	catalog := &typeCatalog{types: make(map[EntityTypeID]entityTypeDefinition, len(definitions))}
	for _, definition := range definitions {
		if definition.id == "" || definition.normalizeSupport == nil || definition.normalizeState == nil || definition.equalState == nil || definition.operations == nil {
			return nil, fmt.Errorf("invalid entity type definition")
		}
		if _, duplicate := catalog.types[definition.id]; duplicate {
			return nil, fmt.Errorf("duplicate entity type %q", definition.id)
		}
		copy := definition
		copy.operations = make(map[OperationName]erasedOperationDefinition, len(definition.operations))
		for name, operation := range definition.operations {
			copy.operations[name] = operation
		}
		catalog.types[definition.id] = copy
	}
	return catalog, nil
}

func (catalog *typeCatalog) normalizeSupport(typeID EntityTypeID, support EntitySupport) (EntitySupport, error) {
	definition, err := catalog.resolve(typeID)
	if err != nil {
		return nil, err
	}
	return definition.normalizeSupport(support)
}

func (catalog *typeCatalog) normalizeState(entity Entity, value Value) (Value, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return nil, err
	}
	return definition.normalizeState(entity.Support, value)
}

func (catalog *typeCatalog) equalState(entity Entity, persisted, incoming Value) (bool, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return false, err
	}
	return definition.equalState(entity.Support, persisted, incoming)
}

func (catalog *typeCatalog) resolveCommand(entity Entity, operationName OperationName, parameters CommandParameters) (resolvedCommand, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return resolvedCommand{}, err
	}
	operation, exists := definition.operations[operationName]
	if !exists {
		return resolvedCommand{}, fmt.Errorf("entity type %q does not define operation %q", entity.TypeID, operationName)
	}
	normalized, err := operation.resolve(entity.Support, parameters)
	if err != nil {
		return resolvedCommand{}, fmt.Errorf("resolve operation %q for entity %q: %w", operationName, entity.ID, err)
	}
	return resolvedCommand{parameters: normalized, deadline: operation.deadline}, nil
}

func (catalog *typeCatalog) satisfies(entity Entity, command commandRecord, value Value) (bool, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return false, err
	}
	operation, exists := definition.operations[command.operationName]
	if !exists {
		return false, fmt.Errorf("entity type %q does not define operation %q", entity.TypeID, command.operationName)
	}
	return operation.satisfies(command.parameters, value)
}

func (catalog *typeCatalog) resolve(typeID EntityTypeID) (entityTypeDefinition, error) {
	if catalog == nil {
		return entityTypeDefinition{}, fmt.Errorf("entity type catalog is nil")
	}
	definition, exists := catalog.types[typeID]
	if !exists {
		return entityTypeDefinition{}, fmt.Errorf("unknown entity type %q", typeID)
	}
	return definition, nil
}
