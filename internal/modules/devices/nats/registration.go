package nats

import (
	"context"
	"errors"
	"log/slog"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type Registrar interface {
	Register(context.Context, string, devices.Registration) (devices.Binding, error)
}

type RegistrationServer struct {
	*requestReplyServer
}

func StartRegistrationServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	registrar Registrar,
	logger *slog.Logger,
) (*RegistrationServer, error) {
	if registrar == nil {
		return nil, errors.New("registration handler is required")
	}
	logger = defaultLogger(logger)
	server, startErr := startRequestReplyServer(
		connection, validator,
		natswire.RegistrationWildcard(), "registration", "registration_id",
		contractsv1.RegistrationRequestSchemaID, contractsv1.RegistrationResponseSchemaID,
		logger,
		func(ctx context.Context, subject string, request natswire.Envelope[registration]) (registrationResponse, bool) {
			route, routeErr := natswire.ParseRegistrationSubject(subject)
			if routeErr != nil {
				logger.ErrorContext(ctx, "discarding registration with invalid subject", "subject", subject, "error", routeErr)
				return registrationResponse{}, false
			}
			response, registrationErr := register(ctx, registrar, route.AdapterID, request.Data)
			if registrationErr != nil {
				logger.ErrorContext(ctx, "handle registration", "subject", subject, "registration_id", request.ID, "error", registrationErr)
				return registrationResponse{}, false
			}
			return response, true
		},
	)
	if startErr != nil {
		return nil, startErr
	}
	return &RegistrationServer{requestReplyServer: server}, nil
}

func register(
	ctx context.Context,
	registrar Registrar,
	adapterID string,
	wire registration,
) (registrationResponse, error) {
	domain := devices.Registration{
		BindingKey: wire.BindingKey,
		Device: devices.DeviceDescriptor{
			ExternalID: copyStringPointer(wire.Device.ExternalID),
			Name:       wire.Device.Name,
			Kind:       devices.DeviceKind(wire.Device.Kind),
		},
		Entities: make([]devices.EntityDescriptor, len(wire.Entities)),
	}
	for index, entity := range wire.Entities {
		domain.Entities[index] = devices.EntityDescriptor{
			Key: entity.Key, ExternalID: entity.ExternalID, Name: entity.Name,
			TypeID:           devices.EntityTypeID(entity.Type),
			Support:          append(devices.EntitySupport(nil), entity.Support...),
			InitiallyEnabled: copyBoolPointer(entity.InitiallyEnabled),
		}
	}
	accepted, err := registrar.Register(ctx, adapterID, domain)
	if rejected, ok := errors.AsType[*devices.RegistrationRejectedError](err); ok {
		return registrationResponse{
			Status: statusRejected,
			Error:  &registrationError{Code: string(rejected.Code), Message: rejected.Message},
		}, nil
	}
	if err != nil {
		return registrationResponse{}, err
	}
	wireBinding := binding{
		BindingKey: accepted.BindingKey,
		DeviceID:   string(accepted.DeviceID),
		Entities:   make([]entityBinding, len(accepted.Entities)),
	}
	for index, entity := range accepted.Entities {
		wireBinding.Entities[index] = entityBinding{
			Key: entity.Key, EntityID: string(entity.EntityID), Enabled: entity.Enabled,
		}
	}
	return registrationResponse{Status: statusAccepted, Binding: &wireBinding}, nil
}

func copyBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
