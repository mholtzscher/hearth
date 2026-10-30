package nats

import (
	"context"
	"errors"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type Registrar interface {
	Register(context.Context, notifications.Registration) (notifications.Destination, error)
}
type registrationRequest struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type registrationResponse struct {
	Status        string `json:"status"`
	DestinationID string `json:"destination_id,omitempty"`
	ReasonCode    string `json:"reason_code,omitempty"`
}

type RegistrationServer struct {
	*platformnats.RequestReplyServer
}

func StartRegistrationServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	registrar Registrar,
	logger *slog.Logger,
) (*RegistrationServer, error) {
	// This module's independent entry point retains its domain error category.
	if connection == nil || validator == nil || registrar == nil {
		return nil, notifications.ErrInvalidInput
	}
	if logger == nil {
		logger = slog.Default()
	}
	server, err := platformnats.StartRequestReplyServer(
		connection,
		validator,
		natswire.NotificationRegistrationWildcard(),
		"notification registration",
		"registration_id",
		contractsv1.NotificationRegistrationRequestSchemaID,
		contractsv1.NotificationRegistrationResponseSchemaID,
		logger,
		func(ctx context.Context, subject string, request natswire.Envelope[registrationRequest]) (registrationResponse, bool) {
			route, routeErr := natswire.ParseNotificationRegistrationSubject(subject)
			if routeErr != nil {
				return registrationResponse{}, false
			}
			ctx, cancel := context.WithTimeout(ctx, notifications.PersistenceTimeout)
			defer cancel()
			destination, registerErr := registrar.Register(ctx, notifications.Registration{
				AdapterID: route.AdapterID, RuntimeID: route.RuntimeID,
				Key: request.Data.Key, Name: request.Data.Name,
			})
			switch {
			case errors.Is(registerErr, notifications.ErrStaleRuntime):
				return registrationResponse{Status: "rejected", ReasonCode: "stale_runtime"}, true
			case errors.Is(registerErr, notifications.ErrInvalidInput):
				return registrationResponse{Status: "rejected", ReasonCode: "invalid_registration"}, true
			case registerErr != nil:
				logger.ErrorContext(ctx, "persist notification registration",
					slog.String("event", "notification.registration_failed"),
					slog.String("stage", "persist"),
					slog.String("error_code", "registration_write_failed"),
					slog.String("registration_id", request.ID),
					slog.String("adapter_id", route.AdapterID),
					slog.String("runtime_id", route.RuntimeID),
				)
				return registrationResponse{}, false
			default:
				return registrationResponse{Status: "accepted", DestinationID: string(destination.ID)}, true
			}
		},
	)
	if err != nil {
		return nil, err
	}
	return &RegistrationServer{RequestReplyServer: server}, nil
}
