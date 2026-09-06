package nats

import (
	"context"
	"errors"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type Registrar interface {
	Register(context.Context, string, devices.RuntimeID, devices.Registration) (devices.Binding, error)
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
		func(
			ctx context.Context,
			subject string,
			request natswire.Envelope[registration],
		) (registrationResponse, bool) {
			route, routeErr := natswire.ParseRegistrationSubject(subject)
			if routeErr != nil {
				logger.With(slog.String("registration_id", request.ID)).WarnContext(ctx,
					"discarding registration with invalid subject",
					slog.String(transportEventKey, "registration.request_discarded"),
					slog.String(transportErrorCodeKey, "subject_invalid"),
				)
				return registrationResponse{}, false
			}
			response, registrationErr := register(
				ctx, registrar, route.AdapterID, devices.RuntimeID(route.RuntimeID), request.Data,
			)
			if registrationErr != nil {
				logger.With(
					slog.String("registration_id", request.ID),
					slog.String("adapter_id", route.AdapterID),
				).ErrorContext(ctx,
					"handle registration",
					slog.String(transportEventKey, "registration.failed"),
					slog.String(transportErrorCodeKey, "registration_failed"),
				)
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
	runtimeID devices.RuntimeID,
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
	accepted, err := registrar.Register(ctx, adapterID, runtimeID, domain)
	if errors.Is(err, devices.ErrRuntimeFenced) {
		return registrationResponse{
			Status: statusRejected,
			Error:  &registrationError{Code: runtimeFencedCode, Message: runtimeFencedMessage},
		}, nil
	}
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
	cloned := *value
	return &cloned
}

func copyStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
