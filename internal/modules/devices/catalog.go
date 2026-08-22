package devices

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/mholtzscher/hearth/entitytypes"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
)

var operationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type OperationDefinition[State, Support any] struct {
	name  OperationName
	build func(*entitytypes.JSONCodec[State], *entitytypes.JSONCodec[Support]) erasedOperationDefinition
	err   error
}

func DefineOperation[State, Support, OperationSupport, Parameters any](
	name OperationName,
	parameters *entitytypes.JSONCodec[Parameters],
	selectSupport func(Support) (OperationSupport, bool),
	validateParameters func(Support, OperationSupport, Parameters) error,
	deadline time.Duration,
	satisfies func(Parameters, State) bool,
) OperationDefinition[State, Support] {
	definition := OperationDefinition[State, Support]{name: name}
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

type EntityTypeDefinition struct {
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

func DefineEntityType[State, Support any](
	id EntityTypeID,
	state *entitytypes.JSONCodec[State],
	support *entitytypes.JSONCodec[Support],
	validateSupportedState func(Support, State) error,
	equalState func(State, State) bool,
	operations ...OperationDefinition[State, Support],
) (EntityTypeDefinition, error) {
	if id == "" {
		return EntityTypeDefinition{}, fmt.Errorf("entity type ID is required")
	}
	if state == nil {
		return EntityTypeDefinition{}, fmt.Errorf("entity type %q has no state codec", id)
	}
	if support == nil {
		return EntityTypeDefinition{}, fmt.Errorf("entity type %q has no support codec", id)
	}
	if validateSupportedState == nil {
		return EntityTypeDefinition{}, fmt.Errorf("entity type %q has no supported-state validator", id)
	}
	if equalState == nil {
		return EntityTypeDefinition{}, fmt.Errorf("entity type %q has no state equality function", id)
	}

	definition := EntityTypeDefinition{
		id:         id,
		operations: make(map[OperationName]erasedOperationDefinition, len(operations)),
	}
	for _, operation := range operations {
		if operation.err != nil {
			return EntityTypeDefinition{}, fmt.Errorf("define entity type %q: %w", id, operation.err)
		}
		if operation.build == nil {
			return EntityTypeDefinition{}, fmt.Errorf("entity type %q has an invalid operation %q", id, operation.name)
		}
		if _, duplicate := definition.operations[operation.name]; duplicate {
			return EntityTypeDefinition{}, fmt.Errorf("entity type %q has duplicate operation %q", id, operation.name)
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
	definition.equalState = func(rawSupport EntitySupport, left, right Value) (bool, error) {
		typedSupport, _, err := support.Decode(json.RawMessage(rawSupport))
		if err != nil {
			return false, fmt.Errorf("invalid support for entity type %q: %w", id, err)
		}
		typedLeft, _, err := state.Decode(json.RawMessage(left))
		if err != nil {
			return false, fmt.Errorf("invalid left state for entity type %q: %w", id, err)
		}
		if err := validateSupportedState(typedSupport, typedLeft); err != nil {
			return false, fmt.Errorf("left state is unsupported by entity type %q: %w", id, err)
		}
		typedRight, _, err := state.Decode(json.RawMessage(right))
		if err != nil {
			return false, fmt.Errorf("invalid right state for entity type %q: %w", id, err)
		}
		if err := validateSupportedState(typedSupport, typedRight); err != nil {
			return false, fmt.Errorf("right state is unsupported by entity type %q: %w", id, err)
		}
		return equalState(typedLeft, typedRight), nil
	}
	return definition, nil
}

type ResolvedCommand struct {
	Parameters CommandParameters
	Deadline   time.Duration
}

type TypeCatalog struct {
	types map[EntityTypeID]EntityTypeDefinition
}

func NewTypeCatalog(definitions []EntityTypeDefinition) (*TypeCatalog, error) {
	catalog := &TypeCatalog{types: make(map[EntityTypeID]EntityTypeDefinition, len(definitions))}
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

func NewFirstLightTypeCatalog() (*TypeCatalog, error) {
	definition, err := newPowerV1TypeDefinition(EntityTypePowerV1)
	if err != nil {
		return nil, err
	}
	return NewTypeCatalog([]EntityTypeDefinition{definition})
}

func newPowerV1TypeDefinition(id EntityTypeID) (EntityTypeDefinition, error) {
	codecs, err := contractpowerv1.Compile()
	if err != nil {
		return EntityTypeDefinition{}, fmt.Errorf("compile power/v1 codecs: %w", err)
	}
	set := DefineOperation(
		OperationNameSet,
		codecs.SetParameters,
		func(support contractpowerv1.Support) (contractpowerv1.SetSupport, bool) {
			return support.Operations.Set, true
		},
		func(contractpowerv1.Support, contractpowerv1.SetSupport, contractpowerv1.SetParameters) error {
			return nil
		},
		10*time.Second,
		func(parameters contractpowerv1.SetParameters, state contractpowerv1.State) bool {
			return parameters.Value == bool(state)
		},
	)
	definition, err := DefineEntityType(
		id,
		codecs.State,
		codecs.Support,
		func(contractpowerv1.Support, contractpowerv1.State) error { return nil },
		func(left, right contractpowerv1.State) bool { return left == right },
		set,
	)
	if err != nil {
		return EntityTypeDefinition{}, err
	}
	return definition, nil
}

func (catalog *TypeCatalog) NormalizeSupport(typeID EntityTypeID, support EntitySupport) (EntitySupport, error) {
	definition, err := catalog.resolve(typeID)
	if err != nil {
		return nil, err
	}
	return definition.normalizeSupport(support)
}

func (catalog *TypeCatalog) NormalizeState(entity Entity, value Value) (Value, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return nil, err
	}
	return definition.normalizeState(entity.Support, value)
}

func (catalog *TypeCatalog) EqualState(entity Entity, left, right Value) (bool, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return false, err
	}
	return definition.equalState(entity.Support, left, right)
}

func (catalog *TypeCatalog) ResolveCommand(entity Entity, operationName OperationName, parameters CommandParameters) (ResolvedCommand, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return ResolvedCommand{}, err
	}
	operation, exists := definition.operations[operationName]
	if !exists {
		return ResolvedCommand{}, fmt.Errorf("entity type %q does not define operation %q", entity.TypeID, operationName)
	}
	normalized, err := operation.resolve(entity.Support, parameters)
	if err != nil {
		return ResolvedCommand{}, fmt.Errorf("resolve operation %q for entity %q: %w", operationName, entity.ID, err)
	}
	return ResolvedCommand{Parameters: normalized, Deadline: operation.deadline}, nil
}

func (catalog *TypeCatalog) Satisfies(entity Entity, command CommandRecord, value Value) (bool, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return false, err
	}
	operation, exists := definition.operations[command.OperationName]
	if !exists {
		return false, fmt.Errorf("entity type %q does not define operation %q", entity.TypeID, command.OperationName)
	}
	return operation.satisfies(command.Parameters, value)
}

func (catalog *TypeCatalog) resolve(typeID EntityTypeID) (EntityTypeDefinition, error) {
	if catalog == nil {
		return EntityTypeDefinition{}, fmt.Errorf("entity type catalog is nil")
	}
	definition, exists := catalog.types[typeID]
	if !exists {
		return EntityTypeDefinition{}, fmt.Errorf("unknown entity type %q", typeID)
	}
	return definition, nil
}
