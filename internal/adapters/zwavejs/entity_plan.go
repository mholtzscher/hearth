// entity_plan.go owns typed Entity plans and immutable route snapshots. Plans bind read and write Value IDs to State
// observation and Command preparation. Generated SDK facades keep vendor JSON behind typed Entity translation.

package zwavejs

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	// commandClassBinarySwitch and commandClassMultilevelSwitch are the only Z-Wave Command Classes v1 plans. Every
	// other Command Class is ignored.
	commandClassBinarySwitch     = 37
	commandClassMultilevelSwitch = 38

	// valuePropertyCurrentValue and valuePropertyTargetValue are the only Value ID property names v1 plans. Any other
	// property, a numeric property name, or a propertyKey-bearing Value is not a candidate and never reaches a plan.
	valuePropertyCurrentValue = "currentValue"
	valuePropertyTargetValue  = "targetValue"

	// metadataTypeBoolean and metadataTypeNumber are the only Value metadata types a plan accepts. The type is
	// validated from metadata alone, including when a target has no current value from which a type could be inferred.
	metadataTypeBoolean = "boolean"
	metadataTypeNumber  = "number"

	// zWaveLevelMaximum is the native Command Class Multilevel Switch maximum that Hearth brightness/v1 mirrors.
	// Hearth's 0..99 State is exactly 100 values, so every canonical State round-trips through 100 native levels.
	zWaveLevelMaximum = 99

	// zWaveRestorePreviousLevel is the Command Class Multilevel Switch restore-previous-level value written for an "on"
	// power Command derived from multilevel state.
	zWaveRestorePreviousLevel = 255

	// maximumPlannedEntitiesPerNode is Hearth's 1..64 Entity registration bound. A node above it is unsupported rather
	// than split across several Devices.
	maximumPlannedEntitiesPerNode = 64

	// maximumDescriptorRunes is Hearth's 1..128 rune descriptor-name bound. A longer name is rejected, never truncated.
	maximumDescriptorRunes = 128

	// maximumEntityKeyBytes is Hearth's 1..63 byte Entity-key bound, matching the subject-safe slug rule.
	maximumEntityKeyBytes = 63

	// interviewStageComplete is the only node interview stage v1 plans.
	interviewStageComplete = "Complete"
)

// entityKind names the Hearth Entity type one plan provides. The zero value is invalid, so a plan that omits its kind
// can never validate.
type entityKind uint8

const (
	// entityKindPower is a hearth.power/v1 Entity.
	entityKindPower entityKind = iota + 1
	// entityKindBrightness is a hearth.brightness/v1 Entity.
	entityKindBrightness
)

// slug is the external-ID path segment, Entity-key suffix, and Device-kind input of one Entity kind. It is empty for an
// unset kind.
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

// displayName is the root Entity display name of one Entity kind. It is empty for an unset kind.
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

// entityPlan binds a Hearth Entity to one endpoint's read and write Value IDs.
//
// A plan holds no cache. Observe never assembles State across frames.
type entityPlan struct {
	// NodeID is the Z-Wave node ID that owns this Entity.
	NodeID int
	// Endpoint is the Z-Wave endpoint index. Root is 0.
	Endpoint int
	// Kind is the Hearth Entity type this plan provides.
	Kind entityKind
	// PowerFromMultilevel records that this power Entity is derived from Multilevel Switch state because the endpoint
	// has no valid Binary Switch pair.
	PowerFromMultilevel bool
	// Descriptor is the registration descriptor of this Entity.
	Descriptor adapter.EntityDescriptor
	// CurrentValueID is the Value ID whose fresh reports are State and the only upstream report eligible for
	// Command-linked evidence.
	CurrentValueID valueID
	// TargetValueID is the Value ID that receives Command writes.
	TargetValueID valueID
	// Observe translates one current Value directly into a typed Observation.
	Observe func(entityID string, receivedAt time.Time, current json.RawMessage) (adapter.Observation, error)
	// PrepareSet validates parameters once and binds the upstream value and matcher.
	PrepareSet func(parameters json.RawMessage) (preparedSet, error)
}

type preparedSet struct {
	Value   json.RawMessage
	Matches func(state json.RawMessage) bool
}

// upstreamValueKey is the exact upstream Value identity used to resolve snapshot and Event values against planned
// Entities. NodeID scopes route lookup to the node that reported the Value, because one Value ID can exist on several
// nodes. Property is the string property name, which excludes numeric properties by construction.
type upstreamValueKey struct {
	NodeID       int
	CommandClass int
	Endpoint     int
	Property     string
}

// valueKey is the node-independent identity of one Value ID. It is used to compare Value identities within one node,
// for example when deciding whether a plan reads and writes the same Value.
func (id valueID) valueKey() upstreamValueKey {
	return upstreamValueKey{
		CommandClass: id.CommandClass,
		Endpoint:     id.Endpoint,
		Property:     id.Property.Name,
	}
}

// routeKey is the route lookup key of one Value ID reported by one node. It includes the node ID, so a Value ID that
// exists on several nodes resolves only against the reporting node's routes and never needs consumer re-filtering.
func (id valueID) routeKey(nodeID int) upstreamValueKey {
	key := id.valueKey()
	key.NodeID = nodeID
	return key
}

// entityRoute is one planned Entity bound to the canonical Hearth Entity ID returned by registration. Live Value Events
// resolve against routes, not plans.
type entityRoute struct {
	// Plan is the immutable planned Entity.
	Plan entityPlan
	// EntityID is the canonical Hearth Entity ID of the successful registration.
	EntityID string
}

// routeSnapshot indexes routes for the active connection generation. The coordinator replaces the table when that
// generation ends.
type routeSnapshot struct {
	// ByEntityID resolves one canonical Entity ID to its route.
	ByEntityID map[string]entityRoute
	// ByValueID resolves one node's current Value ID to every route of that node that projects it, in plan order. The
	// node ID is part of the key, so two nodes that report the same Value ID never share routes. A Multilevel Switch
	// that owns both power and brightness resolves one Value to two routes with power first.
	ByValueID map[upstreamValueKey][]entityRoute
}

// routesForValue returns projecting Entities in plan order. The node-scoped key prevents cross-node routing. Target
// Values and unplanned Command Classes yield no routes and never become State.
func (snapshot routeSnapshot) routesForValue(nodeID int, id valueID) []entityRoute {
	return snapshot.ByValueID[id.routeKey(nodeID)]
}

// powerPlanInput is the shared construction input of the Binary Switch power Entity and the Multilevel Switch derived
// power Entity. Only the read and write Value IDs and the current-value translation differ.
type powerPlanInput struct {
	HomeID   string
	NodeID   int
	Endpoint int
	Label    string
	Current  *valueState
	Target   *valueState
	// FromMultilevel records that a Multilevel Switch owns power because the endpoint has no valid Binary Switch pair.
	FromMultilevel bool
	// DecodeCurrent maps one upstream current Value to a power State. It is decodeBinaryPowerState for a Binary Switch
	// and decodeMultilevelPowerState for a derived Multilevel Switch.
	DecodeCurrent func(current json.RawMessage) (bool, error)
	// EncodeValue renders the upstream Value of one power set Command: a JSON boolean for a Binary Switch and 0 or 255
	// for a Multilevel Switch.
	EncodeValue func(value bool) json.RawMessage
}

// brightnessPlanInput is the construction input of one Multilevel Switch brightness Entity.
type brightnessPlanInput struct {
	HomeID   string
	NodeID   int
	Endpoint int
	Label    string
	Current  *valueState
	Target   *valueState
}

// newPowerEntityPlan builds one hearth.power/v1 plan through the generated powerv1 descriptor facade.
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
		PowerFromMultilevel: input.FromMultilevel,
		Descriptor:          descriptor,
		CurrentValueID:      input.Current.valueID,
		TargetValueID:       input.Target.valueID,
		Observe: func(entityID string, receivedAt time.Time, current json.RawMessage) (adapter.Observation, error) {
			value, decodeErr := input.DecodeCurrent(current)
			if decodeErr != nil {
				return adapter.Observation{}, decodeErr
			}
			return sdkpowerv1.NewObservation(
				sdkpowerv1.ObservationInput{
					EntityID:          entityID,
					Support:           powerSupport(),
					State:             contractpowerv1.State(value),
					AdapterReceivedAt: receivedAt,
				},
			)
		},
		PrepareSet: func(raw json.RawMessage) (preparedSet, error) {
			parameters, decodeErr := decodePowerSetParameters(raw)
			if decodeErr != nil {
				return preparedSet{}, decodeErr
			}
			return preparedSet{Value: input.EncodeValue(parameters.Value), Matches: func(state json.RawMessage) bool {
				observed, stateErr := decodeBooleanStateJSON(state)
				return stateErr == nil && contractpowerv1.SetSatisfied(parameters, contractpowerv1.State(observed))
			}}, nil
		},
	}, nil
}

// newBrightnessEntityPlan builds one hearth.brightness/v1 plan through the generated brightnessv1 descriptor facade,
// with native maximum 99 and step 1.
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
		Descriptor:     descriptor,
		CurrentValueID: input.Current.valueID,
		TargetValueID:  input.Target.valueID,
		Observe: func(entityID string, receivedAt time.Time, current json.RawMessage) (adapter.Observation, error) {
			level, decodeErr := decodeZwaveLevel(current)
			if decodeErr != nil {
				return adapter.Observation{}, decodeErr
			}
			return sdkbrightnessv1.NewObservation(
				sdkbrightnessv1.ObservationInput{
					EntityID:          entityID,
					Support:           brightnessSupport(),
					State:             contractbrightnessv1.State(level),
					AdapterReceivedAt: receivedAt,
				},
			)
		},
		PrepareSet: func(raw json.RawMessage) (preparedSet, error) {
			parameters, decodeErr := decodeBrightnessSetParameters(raw)
			if decodeErr != nil {
				return preparedSet{}, decodeErr
			}
			return preparedSet{
				Value: encodeBrightnessState(parameters.Value),
				Matches: func(state json.RawMessage) bool {
					observed, stateErr := decodeZwaveLevel(state)
					return stateErr == nil &&
						contractbrightnessv1.SetSatisfied(parameters, contractbrightnessv1.State(observed))
				},
			}, nil
		},
	}, nil
}

// powerSupport is the fixed hearth.power/v1 support of every planned power Entity.
func powerSupport() sdkpowerv1.Support {
	return sdkpowerv1.Support{
		State:      sdkpowerv1.StateSupport{},
		Operations: sdkpowerv1.OperationSupport{Set: sdkpowerv1.SetSupport{}},
	}
}

// brightnessSupport is the fixed hearth.brightness/v1 support of every planned Multilevel Switch Entity: native maximum
// 99 and step 1.
func brightnessSupport() sdkbrightnessv1.Support {
	return sdkbrightnessv1.Support{
		State:      sdkbrightnessv1.StateSupport{Maximum: zWaveLevelMaximum},
		Operations: sdkbrightnessv1.OperationSupport{Set: sdkbrightnessv1.SetSupport{Step: 1}},
	}
}

// decodePowerSetParameters decodes one hearth.power/v1 set Command's parameters through the generated schema codec and
// validates its support.
func decodePowerSetParameters(parameters json.RawMessage) (contractpowerv1.SetParameters, error) {
	codecs, err := contractpowerv1.Compile()
	if err != nil {
		return contractpowerv1.SetParameters{}, err
	}
	decoded, _, err := codecs.SetParameters.Decode(parameters)
	if err == nil {
		err = contractpowerv1.ValidateSetParameters(
			powerSupport(),
			contractpowerv1.SetSupport{},
			decoded,
		)
	}
	return decoded, err
}

// decodeBrightnessSetParameters decodes one hearth.brightness/v1 set Command's parameters through the generated
// brightnessv1 codec, including the maximum and step validation.
func decodeBrightnessSetParameters(parameters json.RawMessage) (contractbrightnessv1.SetParameters, error) {
	codecs, err := contractbrightnessv1.Compile()
	if err != nil {
		return contractbrightnessv1.SetParameters{}, err
	}
	decoded, _, err := codecs.SetParameters.Decode(parameters)
	if err == nil {
		support := brightnessSupport()
		err = contractbrightnessv1.ValidateSetParameters(
			support,
			support.Operations.Set,
			decoded,
		)
	}
	return decoded, err
}

// bindEntityRoutes pairs plans with registered Entity IDs in plan order. It rejects mismatched Bindings, missing
// Entities, and missing or repeated IDs.
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
		entity, found := byKey[plan.Descriptor.Key]
		if !found {
			return nil, errors.New("zwavejs: registration omitted planned Entity key " + plan.Descriptor.Key)
		}
		if _, duplicate := bound[entity.EntityID]; duplicate {
			return nil, errors.New("zwavejs: registration repeated Entity ID " + entity.EntityID)
		}
		bound[entity.EntityID] = struct{}{}
		routes = append(routes, entityRoute{Plan: plan, EntityID: entity.EntityID})
	}
	return routes, nil
}

// newRouteSnapshot indexes bound routes by canonical Entity ID and by current Value ID. It refuses a route without an
// Entity ID, a repeated Entity ID, or a plan without an Entity key, so a partial registration can never install a route
// table. An empty route set is valid: a network with no eligible node is healthy.
func newRouteSnapshot(routes []entityRoute) (routeSnapshot, error) {
	snapshot := routeSnapshot{
		ByEntityID: make(map[string]entityRoute, len(routes)),
		ByValueID:  make(map[upstreamValueKey][]entityRoute, len(routes)),
	}
	for _, route := range routes {
		if route.EntityID == "" {
			return routeSnapshot{}, errors.New("zwavejs: route has no canonical Entity ID")
		}
		if route.Plan.Descriptor.Key == "" {
			return routeSnapshot{}, errors.New("zwavejs: route plan has no Entity key")
		}
		if _, duplicate := snapshot.ByEntityID[route.EntityID]; duplicate {
			return routeSnapshot{}, errors.New("zwavejs: duplicate route for Entity " + route.EntityID)
		}
		snapshot.ByEntityID[route.EntityID] = route
		key := route.Plan.CurrentValueID.routeKey(route.Plan.NodeID)
		snapshot.ByValueID[key] = append(snapshot.ByValueID[key], route)
	}
	return snapshot, nil
}

// validateEntityPlans enforces every pre-registration plan invariant: complete descriptors, subject-safe unique keys,
// unique external IDs, bounded names, distinct read and write Value IDs, and every translator present.
func validateEntityPlans(plans []entityPlan) error {
	keys := make(map[string]struct{}, len(plans))
	externalIDs := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if err := validateEntityPlan(plan); err != nil {
			return err
		}
		if _, duplicate := keys[plan.Descriptor.Key]; duplicate {
			return errors.New("zwavejs: duplicate Entity key " + plan.Descriptor.Key)
		}
		keys[plan.Descriptor.Key] = struct{}{}
		if _, duplicate := externalIDs[plan.Descriptor.ExternalID]; duplicate {
			return errors.New("zwavejs: duplicate Entity external ID " + plan.Descriptor.ExternalID)
		}
		externalIDs[plan.Descriptor.ExternalID] = struct{}{}
	}
	return nil
}

// validateEntityPlan enforces the invariants of one plan. The descriptor must be the sole identity used by registration
// and routes.
func validateEntityPlan(plan entityPlan) error {
	switch {
	case plan.Kind != entityKindPower && plan.Kind != entityKindBrightness:
		return errors.New("zwavejs: Entity plan has no Entity kind")
	case !validEntityKey(plan.Descriptor.Key):
		return errors.New("zwavejs: Entity plan has an invalid Entity key")
	case plan.Descriptor.Name == "" || utf8.RuneCountInString(plan.Descriptor.Name) > maximumDescriptorRunes:
		return errors.New("zwavejs: Entity plan has an out-of-bounds Entity name")
	case plan.Descriptor.ExternalID == "":
		return errors.New("zwavejs: Entity plan has no Entity external ID")
	case plan.Descriptor.Type == "" || len(plan.Descriptor.Support) == 0:
		return errors.New("zwavejs: Entity plan descriptor is incomplete")
	case plan.CurrentValueID.valueKey() == plan.TargetValueID.valueKey():
		return errors.New("zwavejs: Entity plan reads and writes the same Value")
	case plan.Observe == nil || plan.PrepareSet == nil:
		return errors.New("zwavejs: Entity plan is missing a translator")
	default:
		return nil
	}
}

// validEntityKey reports whether one Entity key satisfies Hearth's subject-safe slug rule: a lowercase alphanumeric
// first character followed by up to 62 lowercase alphanumerics, "_", or "-".
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

// isLowerAlphaNumericByte reports whether one byte is a lowercase ASCII letter or a decimal digit.
func isLowerAlphaNumericByte(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
}
