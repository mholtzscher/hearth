// entity_plan.go owns the private typed Entity plans and the immutable route
// snapshot. One plan binds a planned Hearth Entity to the exact upstream Value
// IDs it reads and writes, the typed State decoder, the Command encoder, and
// the outcome matcher. Concrete construction goes through the generated
// sdk/adapter/powerv1 and sdk/adapter/brightnessv1 facades, so vendor JSON never
// crosses this boundary without a typed Entity-type hop.

package zwavejs

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const (
	// commandClassBinarySwitch and commandClassMultilevelSwitch are the only
	// Z-Wave Command Classes v1 plans. Every other Command Class is ignored.
	commandClassBinarySwitch     = 37
	commandClassMultilevelSwitch = 38

	// valuePropertyCurrentValue and valuePropertyTargetValue are the only Value
	// ID property names v1 plans. Any other property, a numeric property name,
	// or a propertyKey-bearing Value is not a candidate and never reaches a plan.
	valuePropertyCurrentValue = "currentValue"
	valuePropertyTargetValue  = "targetValue"

	// metadataTypeBoolean and metadataTypeNumber are the only Value metadata
	// types a plan accepts. The type is validated from metadata alone, including
	// when a target has no current value from which a type could be inferred.
	metadataTypeBoolean = "boolean"
	metadataTypeNumber  = "number"

	// zWaveLevelMaximum is the native Command Class Multilevel Switch maximum
	// that Hearth brightness/v1 mirrors. Hearth's 0..99 State is exactly 100
	// values, so every canonical State round-trips through 100 native levels.
	zWaveLevelMaximum = 99

	// zWaveRestorePreviousLevel is the Command Class Multilevel Switch
	// restore-previous-level value written for an "on" power Command derived
	// from multilevel state.
	zWaveRestorePreviousLevel = 255

	// maximumPlannedEntitiesPerNode is Hearth's 1..64 Entity registration bound.
	// A node above it is unsupported rather than split across several Devices.
	maximumPlannedEntitiesPerNode = 64

	// maximumDescriptorRunes is Hearth's 1..128 rune descriptor-name bound. A
	// longer name is rejected, never truncated.
	maximumDescriptorRunes = 128

	// maximumEntityKeyBytes is Hearth's 1..63 byte Entity-key bound, matching the
	// subject-safe slug rule.
	maximumEntityKeyBytes = 63

	// interviewStageComplete is the only node interview stage v1 plans.
	interviewStageComplete = "Complete"

	// probeSetEntityID, probeSetOperation, and probeSetDeadline are the synthetic
	// correlation fields used to drive one generated facade command handler for
	// parameter decoding only. Nothing is dispatched: the handler captures the
	// decoded parameters, and the synthetic identity never reaches a Responder.
	probeSetEntityID  = "probe"
	probeSetOperation = "set"
	probeSetDeadline  = "1970-01-01T00:00:00Z"
)

// entityKind names the Hearth Entity type one plan provides. The zero value is
// invalid, so a plan that omits its kind can never validate.
type entityKind uint8

const (
	// entityKindPower is a hearth.power/v1 Entity.
	entityKindPower entityKind = iota + 1
	// entityKindBrightness is a hearth.brightness/v1 Entity.
	entityKindBrightness
)

// slug is the external-ID path segment, Entity-key suffix, and Device-kind input
// of one Entity kind. It is empty for an unset kind.
func (kind entityKind) slug() string {
	switch kind {
	case entityKindPower:
		return "power"
	case entityKindBrightness:
		return "brightness"
	default:
		return ""
	}
}

// displayName is the root Entity display name of one Entity kind. It is empty
// for an unset kind.
func (kind entityKind) displayName() string {
	switch kind {
	case entityKindPower:
		return "Power"
	case entityKindBrightness:
		return "Brightness"
	default:
		return ""
	}
}

// entityPlan is one immutable planned Hearth Entity for one Z-Wave node
// endpoint: its stable Hearth identity plus the typed translation of the exact
// upstream Value IDs it reads and writes.
//
// A plan holds no cache. DecodeState evaluates exactly the frame it is given, so
// no State is assembled across frames.
type entityPlan struct {
	// NodeID is the Z-Wave node ID that owns this Entity.
	NodeID int
	// Endpoint is the Z-Wave endpoint index. Root is 0.
	Endpoint int
	// Kind is the Hearth Entity type this plan provides.
	Kind entityKind
	// Key is the Hearth Entity key within the node's one registration. It equals
	// Descriptor.Key.
	Key string
	// ExternalID is the stable Hearth Entity external ID. It equals
	// Descriptor.ExternalID.
	ExternalID string
	// Name is the Entity display name. It equals Descriptor.Name.
	Name string
	// PowerFromMultilevel records that this power Entity is derived from
	// Multilevel Switch state because the endpoint has no valid Binary Switch
	// pair.
	PowerFromMultilevel bool
	// Descriptor is the registration descriptor of this Entity.
	Descriptor adapter.EntityDescriptor
	// CurrentValueID is the Value ID whose fresh reports are State and the only
	// upstream report eligible for Command-linked evidence.
	CurrentValueID valueID
	// TargetValueID is the Value ID that receives Command writes.
	TargetValueID valueID
	// DecodeState translates one upstream current Value into the typed State JSON
	// of this Entity's Hearth type. An error means the value is not a
	// representable State for this Entity alone.
	DecodeState func(current json.RawMessage) (json.RawMessage, error)
	// EncodeSet translates one Hearth set Command into the exact upstream JSON
	// value written to TargetValueID. It validates the parameters through the
	// same generated facade the production dispatch path uses.
	EncodeSet func(command adapter.Command) (json.RawMessage, error)
	// Matches reports whether one translated typed State satisfies one Hearth set
	// Command. State is the exact typed State JSON one Observation carries, so a
	// caller can match a published or polled Observation without re-deriving it. A
	// decode error is never a match.
	Matches func(parameters json.RawMessage, state json.RawMessage) bool
}

// upstreamValueKey is the exact upstream Value ID identity used to resolve
// snapshot and Event values against planned Entities. Property is the string
// property name, which excludes numeric properties by construction.
type upstreamValueKey struct {
	CommandClass int
	Endpoint     int
	Property     string
}

// valueKey is the resolution key of one Value ID.
func (id valueID) valueKey() upstreamValueKey {
	return upstreamValueKey{
		CommandClass: id.CommandClass,
		Endpoint:     id.Endpoint,
		Property:     id.Property.Name,
	}
}

// entityRoute is one planned Entity bound to the canonical Hearth Entity ID
// returned by registration. Live Value Events resolve against routes, not plans.
type entityRoute struct {
	// Plan is the immutable planned Entity.
	Plan entityPlan
	// EntityID is the canonical Hearth Entity ID of the successful registration.
	EntityID string
}

// routeSnapshot is the immutable route table for one connection generation and
// one plan revision. It is built once, never mutated, and replaced wholesale, so
// a live Event can only resolve against routes that were active when it arrived.
type routeSnapshot struct {
	// Generation is the connection generation that produced the routes.
	Generation uint64
	// Revision increases with every route replacement within one generation.
	Revision uint64
	// ByEntityID resolves one canonical Entity ID to its route.
	ByEntityID map[string]entityRoute
	// ByValueID resolves one current Value ID to every route that projects it, in
	// plan order. A Multilevel Switch that owns both power and brightness
	// resolves one Value to two routes with power first.
	ByValueID map[upstreamValueKey][]entityRoute
}

// routesForValue returns every planned Entity that projects one current Value,
// in plan order. An empty result means the Value ID is not planned, so a
// targetValue report or an unplanned Command Class is never State.
func (snapshot routeSnapshot) routesForValue(id valueID) []entityRoute {
	return snapshot.ByValueID[id.valueKey()]
}

// powerPlanInput is the shared construction input of the Binary Switch power
// Entity and the Multilevel Switch derived power Entity. Only the read and write
// Value IDs and the current-value translation differ.
type powerPlanInput struct {
	HomeID   string
	NodeID   int
	Endpoint int
	Label    string
	Current  *valueState
	Target   *valueState
	// FromMultilevel records that a Multilevel Switch owns power because the
	// endpoint has no valid Binary Switch pair.
	FromMultilevel bool
	// DecodeCurrent maps one upstream current Value to a power State. It is
	// decodeBinaryPowerState for a Binary Switch and decodeMultilevelPowerState
	// for a derived Multilevel Switch.
	DecodeCurrent func(current json.RawMessage) (bool, error)
	// EncodeValue renders the upstream Value of one power set Command: a JSON
	// boolean for a Binary Switch and 0 or 255 for a Multilevel Switch.
	EncodeValue func(value bool) json.RawMessage
}

// brightnessPlanInput is the construction input of one Multilevel Switch
// brightness Entity.
type brightnessPlanInput struct {
	HomeID   string
	NodeID   int
	Endpoint int
	Label    string
	Current  *valueState
	Target   *valueState
}

// newPowerEntityPlan builds one hearth.power/v1 plan through the generated
// powerv1 descriptor facade.
func newPowerEntityPlan(input powerPlanInput) (entityPlan, error) {
	metadata := entityMetadata(input.HomeID, input.NodeID, input.Endpoint, entityKindPower, input.Label)
	descriptor, err := sdkpowerv1.NewEntityDescriptor(metadata, powerSupport())
	if err != nil {
		return entityPlan{}, err
	}
	return entityPlan{
		NodeID:              input.NodeID,
		Endpoint:            input.Endpoint,
		Kind:                entityKindPower,
		Key:                 metadata.Key,
		ExternalID:          metadata.ExternalID,
		Name:                metadata.Name,
		PowerFromMultilevel: input.FromMultilevel,
		Descriptor:          descriptor,
		CurrentValueID:      input.Current.valueID,
		TargetValueID:       input.Target.valueID,
		DecodeState: func(current json.RawMessage) (json.RawMessage, error) {
			value, decodeErr := input.DecodeCurrent(current)
			if decodeErr != nil {
				return nil, decodeErr
			}
			return encodePowerState(value), nil
		},
		EncodeSet: func(command adapter.Command) (json.RawMessage, error) {
			parameters, decodeErr := decodePowerSetParameters(command.Parameters)
			if decodeErr != nil {
				return nil, decodeErr
			}
			return input.EncodeValue(parameters.Value), nil
		},
		Matches: powerSetMatcher(),
	}, nil
}

// newBrightnessEntityPlan builds one hearth.brightness/v1 plan through the
// generated brightnessv1 descriptor facade, with native maximum 99 and step 1.
func newBrightnessEntityPlan(input brightnessPlanInput) (entityPlan, error) {
	metadata := entityMetadata(input.HomeID, input.NodeID, input.Endpoint, entityKindBrightness, input.Label)
	descriptor, err := sdkbrightnessv1.NewEntityDescriptor(metadata, brightnessSupport())
	if err != nil {
		return entityPlan{}, err
	}
	return entityPlan{
		NodeID:         input.NodeID,
		Endpoint:       input.Endpoint,
		Kind:           entityKindBrightness,
		Key:            metadata.Key,
		ExternalID:     metadata.ExternalID,
		Name:           metadata.Name,
		Descriptor:     descriptor,
		CurrentValueID: input.Current.valueID,
		TargetValueID:  input.Target.valueID,
		DecodeState: func(current json.RawMessage) (json.RawMessage, error) {
			level, decodeErr := decodeZwaveLevel(current)
			if decodeErr != nil {
				return nil, decodeErr
			}
			return encodeBrightnessState(level), nil
		},
		EncodeSet: func(command adapter.Command) (json.RawMessage, error) {
			parameters, decodeErr := decodeBrightnessSetParameters(command.Parameters)
			if decodeErr != nil {
				return nil, decodeErr
			}
			return encodeBrightnessState(parameters.Value), nil
		},
		Matches: brightnessSetMatcher(),
	}, nil
}

// powerSupport is the fixed hearth.power/v1 support of every planned power
// Entity.
func powerSupport() sdkpowerv1.Support {
	return sdkpowerv1.Support{
		State:      sdkpowerv1.StateSupport{},
		Operations: sdkpowerv1.OperationSupport{Set: sdkpowerv1.SetSupport{}},
	}
}

// brightnessSupport is the fixed hearth.brightness/v1 support of every planned
// Multilevel Switch Entity: native maximum 99 and step 1.
func brightnessSupport() sdkbrightnessv1.Support {
	return sdkbrightnessv1.Support{
		State:      sdkbrightnessv1.StateSupport{Maximum: zWaveLevelMaximum},
		Operations: sdkbrightnessv1.OperationSupport{Set: sdkbrightnessv1.SetSupport{Step: 1}},
	}
}

// powerSetMatcher is the outcome predicate shared by both power plan shapes.
func powerSetMatcher() func(parameters, state json.RawMessage) bool {
	return func(parameters, state json.RawMessage) bool {
		set, err := decodePowerSetParameters(parameters)
		if err != nil {
			return false
		}
		observed, err := decodeBooleanStateJSON(state)
		if err != nil {
			return false
		}
		return contractpowerv1.SetSatisfied(set, contractpowerv1.State(observed))
	}
}

// brightnessSetMatcher is the outcome predicate of every brightness plan.
func brightnessSetMatcher() func(parameters, state json.RawMessage) bool {
	return func(parameters, state json.RawMessage) bool {
		set, err := decodeBrightnessSetParameters(parameters)
		if err != nil {
			return false
		}
		observed, err := decodeZwaveLevel(state)
		if err != nil {
			return false
		}
		return contractbrightnessv1.SetSatisfied(set, contractbrightnessv1.State(observed))
	}
}

// decodePowerSetParameters decodes one hearth.power/v1 set Command's parameters
// through the generated powerv1 command handler, so plan translation and
// production dispatch accept exactly the same parameters.
func decodePowerSetParameters(parameters json.RawMessage) (contractpowerv1.SetParameters, error) {
	var decoded contractpowerv1.SetParameters
	handler, err := sdkpowerv1.NewCommandHandler(probeSetEntityID, powerSupport(), sdkpowerv1.Handlers{
		Set: func(
			_ context.Context,
			command typed.Command[contractpowerv1.SetParameters],
			_ adapter.Responder,
		) error {
			decoded = command.Parameters
			return nil
		},
	})
	if err != nil {
		return contractpowerv1.SetParameters{}, err
	}
	if err = handler(context.Background(), probeSetCommand(parameters), nil); err != nil {
		return contractpowerv1.SetParameters{}, err
	}
	return decoded, nil
}

// decodeBrightnessSetParameters decodes one hearth.brightness/v1 set Command's
// parameters through the generated brightnessv1 command handler, including the
// maximum and step validation.
func decodeBrightnessSetParameters(parameters json.RawMessage) (contractbrightnessv1.SetParameters, error) {
	var decoded contractbrightnessv1.SetParameters
	handler, err := sdkbrightnessv1.NewCommandHandler(
		probeSetEntityID,
		brightnessSupport(),
		sdkbrightnessv1.Handlers{
			Set: func(
				_ context.Context,
				command typed.Command[contractbrightnessv1.SetParameters],
				_ adapter.Responder,
			) error {
				decoded = command.Parameters
				return nil
			},
		},
	)
	if err != nil {
		return contractbrightnessv1.SetParameters{}, err
	}
	if err = handler(context.Background(), probeSetCommand(parameters), nil); err != nil {
		return contractbrightnessv1.SetParameters{}, err
	}
	return decoded, nil
}

// probeSetCommand wraps one parameter payload as the synthetic Command a facade
// command handler decodes. Only Parameters reaches the handler.
func probeSetCommand(parameters json.RawMessage) adapter.Command {
	return adapter.Command{
		ID:            probeSetEntityID,
		EntityID:      probeSetEntityID,
		OperationName: probeSetOperation,
		Parameters:    parameters,
		Deadline:      probeSetDeadline,
	}
}

// bindEntityRoutes pairs the plans of one node with the canonical Entity IDs of
// its successful registration, in registration order and plan order. It refuses
// a binding that answers a different Binding, omits a planned Entity, repeats an
// Entity ID, or leaves one without an ID.
func bindEntityRoutes(binding adapter.Binding, node discoveredNode) ([]entityRoute, error) {
	if binding.BindingKey != node.Registration.BindingKey {
		return nil, errors.New(
			"zwavejs: registration answered Binding " + binding.BindingKey +
				" for " + node.Registration.BindingKey,
		)
	}
	if len(binding.Entities) != len(node.Plans) {
		return nil, errors.New(
			"zwavejs: registration answered " + strconv.Itoa(len(binding.Entities)) +
				" Entities for " + strconv.Itoa(len(node.Plans)) + " plans",
		)
	}
	byKey := make(map[string]adapter.EntityBinding, len(binding.Entities))
	for _, entity := range binding.Entities {
		if entity.EntityID == "" {
			return nil, errors.New("zwavejs: registration answered an Entity without an ID")
		}
		if _, duplicate := byKey[entity.Key]; duplicate {
			return nil, errors.New("zwavejs: registration answered duplicate Entity key " + entity.Key)
		}
		byKey[entity.Key] = entity
	}
	routes := make([]entityRoute, 0, len(node.Plans))
	bound := make(map[string]struct{}, len(node.Plans))
	for _, plan := range node.Plans {
		entity, found := byKey[plan.Key]
		if !found {
			return nil, errors.New("zwavejs: registration omitted planned Entity key " + plan.Key)
		}
		if _, duplicate := bound[entity.EntityID]; duplicate {
			return nil, errors.New("zwavejs: registration repeated Entity ID " + entity.EntityID)
		}
		bound[entity.EntityID] = struct{}{}
		routes = append(routes, entityRoute{Plan: plan, EntityID: entity.EntityID})
	}
	return routes, nil
}

// newRouteSnapshot indexes bound routes by canonical Entity ID and by current
// Value ID. It refuses a route without an Entity ID, a repeated Entity ID, or a
// plan without an Entity key, so a partial registration can never install a
// route table. An empty route set is valid: a network with no eligible node is
// healthy.
func newRouteSnapshot(generation, revision uint64, routes []entityRoute) (routeSnapshot, error) {
	snapshot := routeSnapshot{
		Generation: generation,
		Revision:   revision,
		ByEntityID: make(map[string]entityRoute, len(routes)),
		ByValueID:  make(map[upstreamValueKey][]entityRoute, len(routes)),
	}
	for _, route := range routes {
		if route.EntityID == "" {
			return routeSnapshot{}, errors.New("zwavejs: route has no canonical Entity ID")
		}
		if route.Plan.Key == "" {
			return routeSnapshot{}, errors.New("zwavejs: route plan has no Entity key")
		}
		if _, duplicate := snapshot.ByEntityID[route.EntityID]; duplicate {
			return routeSnapshot{}, errors.New("zwavejs: duplicate route for Entity " + route.EntityID)
		}
		snapshot.ByEntityID[route.EntityID] = route
		key := route.Plan.CurrentValueID.valueKey()
		snapshot.ByValueID[key] = append(snapshot.ByValueID[key], route)
	}
	return snapshot, nil
}

// validateEntityPlans enforces every pre-registration plan invariant: complete
// descriptors, subject-safe unique keys, unique external IDs, bounded names,
// distinct read and write Value IDs, and every translator present.
func validateEntityPlans(plans []entityPlan) error {
	keys := make(map[string]struct{}, len(plans))
	externalIDs := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if err := validateEntityPlan(plan); err != nil {
			return err
		}
		if _, duplicate := keys[plan.Key]; duplicate {
			return errors.New("zwavejs: duplicate Entity key " + plan.Key)
		}
		keys[plan.Key] = struct{}{}
		if _, duplicate := externalIDs[plan.ExternalID]; duplicate {
			return errors.New("zwavejs: duplicate Entity external ID " + plan.ExternalID)
		}
		externalIDs[plan.ExternalID] = struct{}{}
	}
	return nil
}

// validateEntityPlan enforces the invariants of one plan. The descriptor must
// agree with the plan identity so a registration can never diverge from the
// route table it installs.
func validateEntityPlan(plan entityPlan) error {
	switch {
	case plan.Kind != entityKindPower && plan.Kind != entityKindBrightness:
		return errors.New("zwavejs: Entity plan has no Entity kind")
	case !validEntityKey(plan.Key):
		return errors.New("zwavejs: Entity plan has an invalid Entity key")
	case plan.Name == "" || utf8.RuneCountInString(plan.Name) > maximumDescriptorRunes:
		return errors.New("zwavejs: Entity plan has an out-of-bounds Entity name")
	case plan.ExternalID == "":
		return errors.New("zwavejs: Entity plan has no Entity external ID")
	case plan.Descriptor.Key != plan.Key ||
		plan.Descriptor.ExternalID != plan.ExternalID ||
		plan.Descriptor.Name != plan.Name:
		return errors.New("zwavejs: Entity plan descriptor disagrees with its identity")
	case plan.Descriptor.Type == "" || len(plan.Descriptor.Support) == 0:
		return errors.New("zwavejs: Entity plan descriptor is incomplete")
	case plan.CurrentValueID.valueKey() == plan.TargetValueID.valueKey():
		return errors.New("zwavejs: Entity plan reads and writes the same Value")
	case plan.DecodeState == nil || plan.EncodeSet == nil || plan.Matches == nil:
		return errors.New("zwavejs: Entity plan is missing a translator")
	default:
		return nil
	}
}

// validEntityKey reports whether one Entity key satisfies Hearth's subject-safe
// slug rule: a lowercase alphanumeric first character followed by up to 62
// lowercase alphanumerics, "_", or "-".
func validEntityKey(key string) bool {
	if key == "" || len(key) > maximumEntityKeyBytes {
		return false
	}
	if !isLowerAlphaNumericByte(key[0]) {
		return false
	}
	for index := 1; index < len(key); index++ {
		character := key[index]
		if !isLowerAlphaNumericByte(character) && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

// isLowerAlphaNumericByte reports whether one byte is a lowercase ASCII letter
// or a decimal digit.
func isLowerAlphaNumericByte(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
}
