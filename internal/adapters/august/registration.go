package august

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/binarysensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter/lockv1"
	"github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
)

func (a *Adapter) loadMappings(ctx context.Context) error {
	const mappingPageSize = 200
	request := adapter.OwnedMappingPageRequest{Limit: mappingPageSize}
	for {
		page, err := a.session.ListOwnedMappings(ctx, request)
		if err != nil {
			return err
		}
		for _, mapping := range page.Items {
			runtime := a.devices[mapping.BindingKey]
			if runtime == nil {
				runtime = newDeviceRuntime()
				a.devices[mapping.BindingKey] = runtime
			}
			runtime.ids[mapping.EntityKey] = mapping.EntityID
		}
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	for _, runtime := range a.devices {
		if err := a.unavailable(ctx, runtime, "initializing"); err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) register(ctx context.Context, runtime *deviceRuntime, d device) error {
	descriptors, err := entityDescriptors(d)
	if err != nil {
		return err
	}
	name := d.Name
	if runtime.config.Name != "" {
		name = runtime.config.Name
	}
	externalID := string(d.ID)
	registration := adapter.Registration{BindingKey: runtime.config.BindingKey,
		Device: adapter.DeviceDescriptor{ExternalID: &externalID, Name: name, Kind: "lock"}, Entities: descriptors}
	binding, err := a.session.Register(ctx, registration)
	if err != nil {
		return err
	}
	if binding.BindingKey != registration.BindingKey || len(binding.Entities) != len(descriptors) {
		return errors.New("august registration mapping mismatch")
	}
	ids := make(map[string]string, len(descriptors))
	for _, entity := range binding.Entities {
		if entity.EntityID == "" || ids[entity.Key] != "" {
			return errors.New("august registration mapping mismatch")
		}
		ids[entity.Key] = entity.EntityID
	}
	for _, descriptor := range descriptors {
		if ids[descriptor.Key] == "" {
			return errors.New("august registration mapping mismatch")
		}
	}
	runtime.device, runtime.ids = d, ids
	return nil
}

func entityMetadata(d device, key, name string) adapter.EntityMetadata {
	return adapter.EntityMetadata{Key: key, ExternalID: string(d.ID) + "/" + key, Name: name}
}

func batterySupport() numericsensorv1.Support {
	const maximumPercent = 100
	quantityKind := "battery_level"
	return numericsensorv1.Support{
		State: numericsensorv1.StateSupport{
			Minimum:      0,
			Maximum:      maximumPercent,
			Unit:         "%",
			QuantityKind: &quantityKind,
		},
	}
}

func entityDescriptors(d device) ([]adapter.EntityDescriptor, error) {
	lock, err := lockv1.NewEntityDescriptor(entityMetadata(d, keyLock, "Lock"), lockv1.Support{})
	if err != nil {
		return nil, err
	}
	door, err := binarysensorv1.NewEntityDescriptor(
		entityMetadata(d, keyDoorOpen, "Door open"),
		binarysensorv1.Support{},
	)
	if err != nil {
		return nil, err
	}
	battery, err := numericsensorv1.NewEntityDescriptor(
		entityMetadata(d, keyBattery, "Reported battery estimate"),
		batterySupport(),
	)
	if err != nil {
		return nil, err
	}
	return []adapter.EntityDescriptor{lock, door, battery}, nil
}
