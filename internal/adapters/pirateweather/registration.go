package pirateweather

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/sdk/adapter"
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
	binding, err := a.session.Register(ctx, registration)
	if err != nil {
		return nil, err
	}
	if binding.BindingKey != externalID || len(binding.Entities) != len(a.measurements) {
		return nil, errors.New("weather registration mapping mismatch")
	}
	ids := make(map[string]string, len(binding.Entities))
	for _, entity := range binding.Entities {
		if ids[entity.Key] != "" || entity.EntityID == "" {
			return nil, errors.New("weather registration mapping mismatch")
		}
		ids[entity.Key] = entity.EntityID
	}
	for _, m := range a.measurements {
		if ids[m.key] == "" {
			return nil, errors.New("weather registration mapping mismatch")
		}
	}
	return ids, nil
}
