package zigbee2mqtt

import (
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	temperatureExposeName  = "temperature"
	temperatureUnitCelsius = "°C"
)

// sensorPlanner supports numeric ambient temperature, humidity, and battery
// root exposes. Temperature uses its milli-Celsius contract; humidity and
// battery share one 0–100 percent numeric-sensor translation through an
// explicit allowlist, so no other numeric expose can register. An eligible
// expose requires its contract unit, a Device-unique property, publish
// access, and no set access. Get access alone controls startup refresh: a
// publish-only sensor has no get properties.
type sensorPlanner struct{}

func (sensorPlanner) Plan(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	appendTemperaturePlans(&contribution, input)
	appendPercentageSensorPlans(&contribution, input, humidityExposeName, "Humidity")
	appendPercentageSensorPlans(&contribution, input, batteryExposeName, "Battery")
	return contribution
}

// appendTemperaturePlans adds one read-only milli-Celsius Entity per
// eligible resolved temperature root, skipping ineligible roots
// individually so valid siblings survive.
func appendTemperaturePlans(contribution *plannerContribution, input devicePlanningInput) {
	for _, root := range input.Exposes.roots {
		if root.expose.Type != upstreamExposeNumeric || root.expose.Name != temperatureExposeName {
			continue
		}
		if !root.resolved {
			continue
		}
		expose := root.expose
		if expose.Unit != temperatureUnitCelsius || !sensorExposeEligible(input, expose) {
			continue
		}
		key, name := scopedIdentity(
			"temperature",
			"Temperature",
			expose.Endpoint,
			root.endpoint,
			root.scoped,
		)
		if !validDescriptorName(name) {
			continue
		}
		plan, err := newTemperaturePlan(adapter.EntityMetadata{
			Key:        key,
			ExternalID: input.IEEE + "/" + entityLocation(root) + "/temperature",
			Name:       name,
		}, expose.Property, exposeCanGet(expose))
		if err != nil {
			continue
		}
		contribution.Entities = append(contribution.Entities, plan)
	}
}

// appendPercentageSensorPlans adds one read-only percent Entity per
// eligible resolved root matching one allowlisted expose name, skipping
// ineligible roots individually so valid siblings survive.
func appendPercentageSensorPlans(
	contribution *plannerContribution,
	input devicePlanningInput,
	exposeName, displayName string,
) {
	for _, root := range input.Exposes.roots {
		if root.expose.Type != upstreamExposeNumeric || root.expose.Name != exposeName {
			continue
		}
		if !root.resolved {
			continue
		}
		expose := root.expose
		if expose.Unit != percentageSensorUnit || !sensorExposeEligible(input, expose) {
			continue
		}
		key, name := scopedIdentity(
			exposeName,
			displayName,
			expose.Endpoint,
			root.endpoint,
			root.scoped,
		)
		if !validDescriptorName(name) {
			continue
		}
		plan, err := newPercentageSensorPlan(adapter.EntityMetadata{
			Key:        key,
			ExternalID: input.IEEE + "/" + entityLocation(root) + "/" + exposeName,
			Name:       name,
		}, expose.Property, exposeCanGet(expose))
		if err != nil {
			continue
		}
		contribution.Entities = append(contribution.Entities, plan)
	}
}

// sensorExposeEligible reports whether one resolved numeric root expose may
// register a read-only sensor: a Device-unique property, publish access,
// and no set access. Callers add their own contract-unit gate.
func sensorExposeEligible(input devicePlanningInput, expose upstreamExpose) bool {
	return expose.Property != "" &&
		exposeCanPublish(expose) && !exposeCanSet(expose) &&
		input.Exposes.PropertyUnique(expose.Property)
}
