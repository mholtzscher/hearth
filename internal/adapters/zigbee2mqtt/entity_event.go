package zigbee2mqtt

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkenumeventv1 "github.com/mholtzscher/hearth/sdk/adapter/enumeventv1"
)

// actionExposeName is the only device-root enum expose name that maps to a
// Hearth Event source. Every other enum root is either a Command action
// (effect, reset_total_energy) or an unplanned setting.
const actionExposeName = "action"

// newActionEventPlan builds the complete read-only action translation for one
// publish-only enum property. The plan claims no State, runs no State
// decoder, requests no refresh, and translates no Commands: every
// non-retained message carrying a supported action value publishes exactly
// one Entity Event through the generated enumevent facade. EventProperties
// names the Event source so the pending replay queue preserves a pre-activation
// occurrence instead of coalescing it away. The constructor validates the
// discovered values as Event names and omits the Entity on error, so an
// unsupported name set never registers.
func newActionEventPlan(
	metadata adapter.EntityMetadata,
	property string,
	names []string,
) (entityPlan, error) {
	support := actionEventSupport(names)
	descriptor, descriptorErr := sdkenumeventv1.NewEntityDescriptor(metadata, support)
	if descriptorErr != nil {
		return entityPlan{}, descriptorErr
	}
	return entityPlan{
		Descriptor:      descriptor,
		StatePolicy:     entityStateless,
		EventProperties: []string{property},
		DecodeEvent:     actionEventDecoder(property, support),
	}, nil
}

func actionEventSupport(names []string) sdkenumeventv1.Support {
	return sdkenumeventv1.Support{
		State:      sdkenumeventv1.StateSupport{},
		Operations: sdkenumeventv1.OperationSupport{},
		Events: sdkenumeventv1.SupportEvents{
			Names: names,
		},
	}
}

// actionEventDecoder maps one present action value to one Entity Event.
// Absent, empty, and null values emit none without an issue; a malformed or
// unsupported value returns an issue, which the caller logs without
// suppressing valid sibling State and without fabricating an Event.
func actionEventDecoder(property string, support sdkenumeventv1.Support) eventDecoder {
	return func(entityID string, properties map[string]json.RawMessage) (adapter.EntityEvent, bool, error) {
		raw, present := properties[property]
		if !present {
			return adapter.EntityEvent{}, false, nil
		}
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return adapter.EntityEvent{}, false, fmt.Errorf("decode action value: %w", err)
		}
		if name == "" {
			return adapter.EntityEvent{}, false, nil
		}
		event, err := sdkenumeventv1.NewEntityEvent(sdkenumeventv1.EntityEventInput{
			EntityID: entityID, Support: support, Name: name,
		})
		if err != nil {
			return adapter.EntityEvent{}, false, err
		}
		return event, true, nil
	}
}

// planActionEvent discovers the optional device-root publish-only action enum
// once per device. Access must be exactly publish-only with a device-unique
// property and a resolvable endpoint; the constructor validates the
// discovered values as generated Event names and omits the Entity on error.
// The contribution is supplemental so a standalone button keeps the sensor
// kind and never creates a command route or startup refresh.
func planActionEvent(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	root, ok := input.Exposes.UniqueRoot(upstreamExposeEnum, actionExposeName)
	if !ok || !root.resolved {
		return contribution
	}
	expose := root.expose
	if !isActionEventProperty(expose.Property) || expose.Access != exposePublishAccessBit ||
		!input.Exposes.PropertyUnique(expose.Property) {
		return contribution
	}
	key, name := scopedIdentity(
		"action",
		"Action",
		expose.Endpoint,
		root.endpoint,
		root.scoped,
	)
	if !validDescriptorName(name) {
		return contribution
	}
	plan, err := newActionEventPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/action",
		Name:       name,
	}, expose.Property, expose.Values)
	if err != nil {
		return contribution
	}
	contribution.Entities = append(contribution.Entities, plan)
	return contribution
}

// isActionEventProperty limits Event sources to the Zigbee2MQTT property
// namespace covered by CACHE_IGNORE_PROPERTIES. Accepting another property
// would let cache-expanded State fabricate occurrences and would let the
// pre-inventory pending queue coalesce real reports.
func isActionEventProperty(property string) bool {
	return property == actionExposeName || strings.HasPrefix(property, actionExposeName+"_")
}
