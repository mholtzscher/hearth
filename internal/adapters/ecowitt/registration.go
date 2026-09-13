package ecowitt

import (
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// errRegistrationMismatch marks a Session registration response that does not
// match the static registration request. It carries no vendor identity.
var errRegistrationMismatch = errors.New("ecowitt registration response does not match its request")

// registrationSlots is the fixed registration order: the GW2000 gateway slot
// first, then the WS90 outdoor array slot.
func registrationSlots() []deviceSlot { return []deviceSlot{gatewaySlot, outdoorArraySlot} }

// entityRoute joins one catalog plan to the canonical Entity ID the Session
// returned for it.
type entityRoute struct {
	Index     int
	Slot      deviceSlot
	EntityKey string
	EntityID  string
	Plan      measurementPlan
}

// routeSnapshot is the canonical traversal order used for Observations and
// availability reports: catalog order, gateway before outdoor array.
type routeSnapshot struct {
	entities []entityRoute
}

// staticRegistrations builds the two slot registrations for one configuration.
// Registration is idempotent and additive, and both Devices are registered
// before any MQTT connection is attempted, so no report is required to
// establish identity.
func staticRegistrations(config Config, plans []measurementPlan) []adapter.Registration {
	return []adapter.Registration{
		slotRegistration(gatewaySlot, config.GatewayName, plans),
		slotRegistration(outdoorArraySlot, config.OutdoorArrayName, plans),
	}
}

// slotRegistration builds one slot's Device and Entity descriptors in catalog
// order.
func slotRegistration(slot deviceSlot, name string, plans []measurementPlan) adapter.Registration {
	owned := plansForSlot(plans, slot)
	descriptors := make([]adapter.EntityDescriptor, 0, len(owned))
	for _, plan := range owned {
		descriptors = append(descriptors, plan.Descriptor)
	}
	externalID := string(slot)
	return adapter.Registration{
		BindingKey: externalID,
		Device: adapter.DeviceDescriptor{
			ExternalID: &externalID,
			Name:       name,
			Kind:       deviceKindSensor,
		},
		Entities: descriptors,
	}
}

// buildRouteSnapshot joins the catalog plans to the Session's canonical
// bindings, in registration order. It rejects a mismatched or incomplete
// binding response rather than publishing against a guessed Entity ID.
func buildRouteSnapshot(plans []measurementPlan, bindings []adapter.Binding) (routeSnapshot, error) {
	if len(bindings) != len(registrationSlots()) {
		return routeSnapshot{}, fmt.Errorf("%w: %d bindings for %d Device slots",
			errRegistrationMismatch, len(bindings), len(registrationSlots()))
	}
	snapshot := routeSnapshot{}
	for index, slot := range registrationSlots() {
		binding := bindings[index]
		if binding.BindingKey != string(slot) {
			return routeSnapshot{}, fmt.Errorf("%w: binding key order mismatch", errRegistrationMismatch)
		}
		owned := plansForSlot(plans, slot)
		if len(binding.Entities) != len(owned) {
			return routeSnapshot{}, fmt.Errorf("%w: Entity count mismatch for one Device slot",
				errRegistrationMismatch)
		}
		byKey := make(map[string]string, len(binding.Entities))
		for _, entity := range binding.Entities {
			if entity.Key == "" || entity.EntityID == "" {
				return routeSnapshot{}, fmt.Errorf("%w: empty Entity mapping", errRegistrationMismatch)
			}
			if _, duplicate := byKey[entity.Key]; duplicate {
				return routeSnapshot{}, fmt.Errorf("%w: duplicate Entity key", errRegistrationMismatch)
			}
			byKey[entity.Key] = entity.EntityID
		}
		for _, plan := range owned {
			entityID, present := byKey[plan.EntityKey]
			if !present {
				return routeSnapshot{}, fmt.Errorf("%w: registration omitted an Entity", errRegistrationMismatch)
			}
			snapshot.entities = append(snapshot.entities, entityRoute{
				Index:     len(snapshot.entities),
				Slot:      slot,
				EntityKey: plan.EntityKey,
				EntityID:  entityID,
				Plan:      plan,
			})
		}
	}
	return snapshot, nil
}
