package zigbee2mqtt

import (
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	temperatureExposeName  = "temperature"
	temperatureUnitCelsius = "°C"
)

// sensorPlanner supports numeric ambient temperature root exposes. An eligible
// expose requires the Celsius unit, a Device-unique property, publish access,
// and no set access. Get access alone controls startup refresh: a
// publish-only sensor has no get properties.
type sensorPlanner struct{}

func (sensorPlanner) Plan(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	for _, root := range input.Exposes.roots {
		if root.expose.Type != upstreamExposeNumeric || root.expose.Name != temperatureExposeName {
			continue
		}
		if !root.resolved {
			continue
		}
		expose := root.expose
		if expose.Unit != temperatureUnitCelsius || expose.Property == "" ||
			!exposeCanPublish(expose) || exposeCanSet(expose) ||
			!input.Exposes.PropertyUnique(expose.Property) {
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
	return contribution
}
