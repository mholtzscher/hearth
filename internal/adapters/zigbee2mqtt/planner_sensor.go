package zigbee2mqtt

import (
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	temperatureExposeName  = "temperature"
	temperatureUnitCelsius = "°C"
	publishAccessMask      = 1
	setAccessMask          = 2
	getAccessMask          = 4
)

// sensorPlanner supports numeric ambient temperature root exposes. An eligible
// expose requires the Celsius unit, a Device-unique property, publish access,
// and no set access. Get access alone controls startup refresh: a
// publish-only sensor has no get properties.
type sensorPlanner struct{}

func (sensorPlanner) Plan(input devicePlanningInput) (plannerContribution, error) {
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor}
	for _, root := range input.Exposes.roots {
		if root.expose.Type != upstreamExposeNumeric || root.expose.Name != temperatureExposeName {
			continue
		}
		if !root.resolved {
			continue
		}
		expose := root.expose
		if expose.Unit != temperatureUnitCelsius || expose.Property == "" ||
			expose.Access&publishAccessMask == 0 || expose.Access&setAccessMask != 0 ||
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
		}, expose.Property, expose.Access&getAccessMask != 0)
		if err != nil {
			continue
		}
		contribution.Entities = append(contribution.Entities, plan)
	}
	return contribution, nil
}
