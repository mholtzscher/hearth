package zigbee2mqtt

import (
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	temperatureExposeName  = "temperature"
	temperatureUnitCelsius = "°C"
)

// sensorPlanner plans temperature first, then the ambient numeric capability
// table in order. Each mapping visits retained roots independently so an
// ineligible root cannot suppress valid siblings. Family order and root
// selection stay in Go; catalog records contain only capability data.
type sensorPlanner struct{}

func (sensorPlanner) Plan(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	appendTemperaturePlans(&contribution, input)
	for _, mapping := range ambientNumericSensors() {
		appendNumericSensorPlans(&contribution, input, mapping)
	}
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

// appendNumericSensorPlans adds a read-only Entity for each eligible root
// matching a capability. Ambient bounds are fixed by the mapping rather
// than inferred from upstream reports.
func appendNumericSensorPlans(
	contribution *plannerContribution,
	input devicePlanningInput,
	mapping numericSensorMapping,
) {
	for _, root := range input.Exposes.roots {
		if plan := planNumericSensorRoot(input, root, mapping); plan != nil {
			contribution.Entities = append(contribution.Entities, *plan)
		}
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
