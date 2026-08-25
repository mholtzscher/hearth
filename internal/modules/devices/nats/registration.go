package nats

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type Registrar interface {
	Register(context.Context, string, devices.Registration) (devices.Binding, error)
}

func RegistrationHandler(registrar Registrar) platformnats.RegistrationHandler {
	return func(ctx context.Context, adapterID string, registration platformnats.Registration) (platformnats.RegistrationResponse, error) {
		domainRegistration := devices.Registration{
			BindingKey: registration.BindingKey,
			Device: devices.DeviceDescriptor{
				ExternalID: registration.Device.ExternalID,
				Name:       registration.Device.Name,
				Kind:       devices.DeviceKind(registration.Device.Kind),
			},
			Entities: make([]devices.EntityDescriptor, len(registration.Entities)),
		}
		for index, entity := range registration.Entities {
			domainRegistration.Entities[index] = devices.EntityDescriptor{
				Key: entity.Key, ExternalID: entity.ExternalID, Name: entity.Name,
				TypeID:  devices.EntityTypeID(entity.Type),
				Support: devices.EntitySupport(append([]byte(nil), entity.Support...)),
			}
		}
		binding, err := registrar.Register(ctx, adapterID, domainRegistration)
		var rejected *devices.RegistrationRejectedError
		if errors.As(err, &rejected) {
			return platformnats.RegistrationResponse{
				Status: "rejected",
				Error:  &platformnats.RegistrationError{Code: string(rejected.Code), Message: rejected.Message},
			}, nil
		}
		if err != nil {
			return platformnats.RegistrationResponse{}, err
		}
		wireBinding := platformnats.Binding{
			BindingKey: binding.BindingKey, DeviceID: string(binding.DeviceID),
			Entities: make([]platformnats.EntityBinding, len(binding.Entities)),
		}
		for index, entity := range binding.Entities {
			wireBinding.Entities[index] = platformnats.EntityBinding{Key: entity.Key, EntityID: string(entity.EntityID)}
		}
		return platformnats.RegistrationResponse{Status: "accepted", Binding: &wireBinding}, nil
	}
}
