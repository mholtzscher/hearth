package pirateweather

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/dailyforecastv1"
	"github.com/mholtzscher/hearth/sdk/adapter/hourlyforecastv1"
)

func (a *Adapter) register(ctx context.Context) (map[string]string, error) {
	externalID := "location"
	registration := adapter.Registration{
		BindingKey: externalID,
		Device: adapter.DeviceDescriptor{
			ExternalID: &externalID,
			Name:       a.client.config.LocationName,
			Kind:       "sensor",
		},
	}
	for _, m := range a.measurements {
		descriptor, err := m.descriptor()
		if err != nil {
			return nil, err
		}
		registration.Entities = append(registration.Entities, descriptor)
	}
	hourly, err := hourlyforecastv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: hourlyForecastKey, ExternalID: "location/" + hourlyForecastKey, Name: "Hourly forecast",
	}, hourlyforecastv1.Support{})
	if err != nil {
		return nil, err
	}
	daily, err := dailyforecastv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: dailyForecastKey, ExternalID: "location/" + dailyForecastKey, Name: "Daily forecast",
	}, dailyforecastv1.Support{})
	if err != nil {
		return nil, err
	}
	registration.Entities = append(registration.Entities, hourly, daily)
	binding, err := a.session.Register(ctx, registration)
	if err != nil {
		return nil, err
	}
	if binding.BindingKey != externalID || len(binding.Entities) != len(registration.Entities) {
		return nil, errors.New("weather registration mapping mismatch")
	}
	ids := make(map[string]string, len(binding.Entities))
	for _, entity := range binding.Entities {
		if ids[entity.Key] != "" || entity.EntityID == "" {
			return nil, errors.New("weather registration mapping mismatch")
		}
		ids[entity.Key] = entity.EntityID
	}
	for _, descriptor := range registration.Entities {
		if ids[descriptor.Key] == "" {
			return nil, errors.New("weather registration mapping mismatch")
		}
	}
	return ids, nil
}
