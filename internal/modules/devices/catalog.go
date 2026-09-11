package devices

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/entitytypes"
)

var operationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type OperationDefinition[State, Support any] struct {
	name  OperationName
	build func(*entitytypes.JSONCodec[State], *entitytypes.JSONCodec[Support]) erasedOperationDefinition
	err   error
}

//nolint:gocognit // Generic boundary validation is kept with the operation definition it protects.
func DefineOperation[State, Support, OperationSupport, Parameters any](
	name OperationName,
	parameters *entitytypes.JSONCodec[Parameters],
	selectSupport func(Support) (OperationSupport, bool),
	validateParameters func(Support, OperationSupport, Parameters) error,
	deadline time.Duration,
	outcome OutcomeKind,
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
	case outcome != OutcomeObserved && outcome != OutcomeDispatched:
		definition.err = fmt.Errorf("operation %q has invalid outcome %q", name, outcome)
	case outcome == OutcomeDispatched && satisfies != nil:
		definition.err = fmt.Errorf("operation %q is dispatched and must have no outcome matcher", name)
	case outcome == OutcomeObserved && satisfies == nil:
		definition.err = fmt.Errorf("operation %q has no outcome matcher", name)
	default:
		definition.build = func(state *entitytypes.JSONCodec[State], support *entitytypes.JSONCodec[Support]) erasedOperationDefinition {
			erased := erasedOperationDefinition{
				outcome:  outcome,
				deadline: deadline,
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
					if validationErr := validateParameters(
						typedSupport, operationSupport, typedParameters,
					); validationErr != nil {
						return nil, fmt.Errorf("validate parameters for operation %q: %w", name, validationErr)
					}
					return CommandParameters(normalized), nil
				},
			}
			if satisfies != nil {
				erased.satisfies = func(rawParameters CommandParameters, rawState Value) (bool, error) {
					typedParameters, _, err := parameters.Decode(json.RawMessage(rawParameters))
					if err != nil {
						return false, fmt.Errorf("decode recorded parameters for operation %q: %w", name, err)
					}
					typedState, _, err := state.Decode(json.RawMessage(rawState))
					if err != nil {
						return false, fmt.Errorf("decode state for operation %q: %w", name, err)
					}
					return satisfies(typedParameters, typedState), nil
				}
			}
			return erased
		}
	}
	return definition
}

type EntityTypeDefinition struct {
	id        EntityTypeID
	stateless bool // from manifest "stateless" (default false)
	// eventNames is nil for a type that is not an Entity Event source. A nil
	// selector classifies the type as a non-event before any support decoding, so
	// a malformed persisted support is never a catalog failure for such a type.
	eventNames       func(EntitySupport) ([]EntityEventName, error)
	normalizeSupport func(EntitySupport) (EntitySupport, error)
	normalizeState   func(EntitySupport, Value) (Value, error)
	equalState       func(EntitySupport, Value, Value) (bool, error)
	operations       map[OperationName]erasedOperationDefinition
}

type erasedOperationDefinition struct {
	resolve   func(EntitySupport, CommandParameters) (CommandParameters, error)
	deadline  time.Duration
	outcome   OutcomeKind
	satisfies func(CommandParameters, Value) (bool, error) // nil iff dispatched
}

// DefineEventSourceEntityType defines a stateless, non-commandable Entity type
// whose only reported occurrences are Entity Events. selectEventNames returns
// the supported names of one already-decoded support; the type's own support
// validator still decides whether that support is accepted at all.
func DefineEventSourceEntityType[State, Support any](
	id EntityTypeID,
	state *entitytypes.JSONCodec[State],
	support *entitytypes.JSONCodec[Support],
	validateSupport func(Support) error,
	validateSupportedState func(Support, State) error,
	equalState func(State, State) bool,
	selectEventNames func(Support) []string,
) (EntityTypeDefinition, error) {
	if selectEventNames == nil {
		return EntityTypeDefinition{}, fmt.Errorf("entity type %q has no Entity Event name selector", id)
	}
	definition, err := DefineEntityType(
		id, state, support, validateSupport, validateSupportedState, equalState,
	)
	if err != nil {
		return EntityTypeDefinition{}, err
	}
	// An event source is stateless and non-commandable: it defines no State
	// space and no Operations, so Entity Events are its only reported input.
	definition.stateless = true
	definition.eventNames = func(rawSupport EntitySupport) ([]EntityEventName, error) {
		typedSupport, _, decodeErr := support.Decode(json.RawMessage(rawSupport))
		if decodeErr != nil {
			return nil, fmt.Errorf("invalid support for entity type %q: %w", id, decodeErr)
		}
		if validationErr := validateSupport(typedSupport); validationErr != nil {
			return nil, fmt.Errorf("unsupported support for entity type %q: %w", id, validationErr)
		}
		selected := selectEventNames(typedSupport)
		names := make([]EntityEventName, 0, len(selected))
		for _, name := range selected {
			names = append(names, EntityEventName(name))
		}
		return names, nil
	}
	return definition, nil
}

//nolint:gocognit // Generic boundary validation is kept with the Entity type definition it protects.
func DefineEntityType[State, Support any](
	id EntityTypeID,
	state *entitytypes.JSONCodec[State],
	support *entitytypes.JSONCodec[Support],
	validateSupport func(Support) error,
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
	if validateSupport == nil {
		return EntityTypeDefinition{}, fmt.Errorf("entity type %q has no support validator", id)
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

	definition.normalizeSupport = makeNormalizeSupport(id, support, validateSupport)
	definition.normalizeState = func(rawSupport EntitySupport, rawState Value) (Value, error) {
		typedSupport, _, err := support.Decode(json.RawMessage(rawSupport))
		if err != nil {
			return nil, fmt.Errorf("invalid support for entity type %q: %w", id, err)
		}
		typedState, normalized, err := state.Decode(json.RawMessage(rawState))
		if err != nil {
			return nil, fmt.Errorf("invalid state for entity type %q: %w", id, err)
		}
		if validationErr := validateSupportedState(typedSupport, typedState); validationErr != nil {
			return nil, fmt.Errorf("state is unsupported by entity type %q: %w", id, validationErr)
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
		if validationErr := validateSupportedState(typedSupport, typedIncoming); validationErr != nil {
			return false, fmt.Errorf("incoming state is unsupported by entity type %q: %w", id, validationErr)
		}
		return equalState(typedPersisted, typedIncoming), nil
	}
	return definition, nil
}

// makeNormalizeSupport decodes support through its codec and enforces the
// generated support_validation rules, so registration and command execution
// reject the same unsupported supports.
func makeNormalizeSupport[Support any](
	id EntityTypeID,
	support *entitytypes.JSONCodec[Support],
	validateSupport func(Support) error,
) func(EntitySupport) (EntitySupport, error) {
	return func(raw EntitySupport) (EntitySupport, error) {
		typed, normalized, decodeErr := support.Decode(json.RawMessage(raw))
		if decodeErr != nil {
			return nil, fmt.Errorf("invalid support for entity type %q: %w", id, decodeErr)
		}
		if validateErr := validateSupport(typed); validateErr != nil {
			return nil, fmt.Errorf("unsupported support for entity type %q: %w", id, validateErr)
		}
		return EntitySupport(normalized), nil
	}
}

type ResolvedCommand struct {
	Parameters CommandParameters
	Deadline   time.Duration
	Outcome    OutcomeKind
}

type TypeCatalog struct {
	types map[EntityTypeID]EntityTypeDefinition
}

func NewTypeCatalog(definitions []EntityTypeDefinition) (*TypeCatalog, error) {
	catalog := &TypeCatalog{types: make(map[EntityTypeID]EntityTypeDefinition, len(definitions))}
	for _, definition := range definitions {
		if definition.id == "" || definition.normalizeSupport == nil || definition.normalizeState == nil ||
			definition.equalState == nil ||
			definition.operations == nil {
			return nil, fmt.Errorf("invalid entity type definition")
		}
		if _, duplicate := catalog.types[definition.id]; duplicate {
			return nil, fmt.Errorf("duplicate entity type %q", definition.id)
		}
		cloned := definition
		cloned.operations = make(map[OperationName]erasedOperationDefinition, len(definition.operations))
		maps.Copy(cloned.operations, definition.operations)
		catalog.types[definition.id] = cloned
	}
	return catalog, nil
}

func (catalog *TypeCatalog) NormalizeSupport(typeID EntityTypeID, support EntitySupport) (EntitySupport, error) {
	definition, err := catalog.resolve(typeID)
	if err != nil {
		return nil, err
	}
	return definition.normalizeSupport(support)
}

func (catalog *TypeCatalog) IsStateless(typeID EntityTypeID) (bool, error) {
	definition, err := catalog.resolve(typeID)
	if err != nil {
		return false, err
	}
	return definition.stateless, nil
}

// SupportsEntityEvent reports whether an Entity's current support accepts one
// Entity Event name. Non-event classification wins before any support decoding:
// a resolved type with no Entity Event selector reports false with no error even
// when its persisted support is malformed, because such a report is an ordinary
// unsupported_event rejection rather than a catalog failure. A type with the
// selector decodes and validates its persisted descriptor, so an unsupported
// name is false with no error while a corrupt event-source descriptor is a
// catalog failure. An unknown type is a catalog failure in both cases. Every
// returned error is a deterministic failure to interpret the persisted
// descriptor, which the repository reports as ErrEntityEventDescriptorCorrupt.
func (catalog *TypeCatalog) SupportsEntityEvent(entity Entity, name EntityEventName) (bool, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return false, err
	}
	if definition.eventNames == nil {
		return false, nil
	}
	names, err := definition.eventNames(entity.Support)
	if err != nil {
		return false, err
	}
	return slices.Contains(names, name), nil
}

func (catalog *TypeCatalog) NormalizeState(entity Entity, value Value) (Value, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return nil, err
	}
	return definition.normalizeState(entity.Support, value)
}

func (catalog *TypeCatalog) EqualState(entity Entity, persisted, incoming Value) (bool, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return false, err
	}
	return definition.equalState(entity.Support, persisted, incoming)
}

func (catalog *TypeCatalog) ResolveCommand(
	entity Entity,
	operationName OperationName,
	parameters CommandParameters,
) (ResolvedCommand, error) {
	definition, err := catalog.resolve(entity.TypeID)
	if err != nil {
		return ResolvedCommand{}, err
	}
	operation, exists := definition.operations[operationName]
	if !exists {
		return ResolvedCommand{}, fmt.Errorf(
			"entity type %q does not define operation %q",
			entity.TypeID,
			operationName,
		)
	}
	if _, normalizeErr := definition.normalizeSupport(entity.Support); normalizeErr != nil {
		return ResolvedCommand{}, normalizeErr
	}
	normalized, err := operation.resolve(entity.Support, parameters)
	if err != nil {
		return ResolvedCommand{}, fmt.Errorf("resolve operation %q for entity %q: %w", operationName, entity.ID, err)
	}
	return ResolvedCommand{
		Parameters: normalized,
		Deadline:   operation.deadline,
		Outcome:    operation.outcome,
	}, nil
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
	if operation.outcome == OutcomeDispatched {
		return false, fmt.Errorf(
			"entity type %q operation %q is dispatched and has no outcome matcher",
			entity.TypeID,
			command.OperationName,
		)
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
