package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	natsgo "github.com/nats-io/nats.go"
)

type Registrar interface {
	Register(context.Context, string, devices.Registration) (devices.Binding, error)
}

type RegistrationServer struct {
	subscription *natsgo.Subscription
}

func StartRegistrationServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	registrar Registrar,
	logger *slog.Logger,
) (*RegistrationServer, error) {
	if connection == nil {
		return nil, errors.New("registration NATS connection is required")
	}
	if validator == nil {
		return nil, errors.New("registration validator is required")
	}
	if registrar == nil {
		return nil, errors.New("registration handler is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	subscription, err := connection.Subscribe(natswire.RegistrationWildcard(), func(message *natsgo.Msg) {
		handleRegistrationMessage(connection, message, validator, registrar, logger)
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe to registrations: %w", err)
	}
	if err := connection.Flush(); err != nil {
		_ = subscription.Unsubscribe()
		return nil, fmt.Errorf("activate registration subscription: %w", err)
	}
	return &RegistrationServer{subscription: subscription}, nil
}

func (server *RegistrationServer) Drain() error {
	if server == nil || server.subscription == nil {
		return nil
	}
	if err := server.subscription.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
		return fmt.Errorf("drain registration subscription: %w", err)
	}
	return nil
}

func handleRegistrationMessage(
	connection *natsgo.Conn,
	message *natsgo.Msg,
	validator *contractsv1.Validator,
	registrar Registrar,
	logger *slog.Logger,
) {
	if message.Reply == "" {
		logger.Error("discarding registration without reply subject", "subject", message.Subject)
		return
	}
	route, err := natswire.ParseRegistrationSubject(message.Subject)
	if err != nil {
		logger.Error("discarding registration with invalid subject", "subject", message.Subject, "error", err)
		return
	}
	request, err := natswire.Decode[registration](validator, contractsv1.RegistrationRequestSchemaID, message.Data)
	if err != nil {
		logger.Error("discarding invalid registration", "subject", message.Subject, "error", err)
		return
	}
	if request.CausationID != nil {
		logger.Error("discarding caused registration", "subject", message.Subject, "registration_id", request.ID)
		return
	}
	ctx := natswire.ExtractTrace(context.Background(), message.Header)
	response, err := register(ctx, registrar, route.AdapterID, request.Data)
	if err != nil {
		logger.Error("handle registration", "subject", message.Subject, "registration_id", request.ID, "error", err)
		return
	}
	replyID, err := newReplyID()
	if err != nil {
		logger.Error("generate registration reply ID", "registration_id", request.ID, "error", err)
		return
	}
	causationID := request.ID
	reply := natswire.Envelope[registrationResponse]{
		ID: replyID, Schema: contractsv1.RegistrationResponseSchemaID,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: request.CorrelationID,
		CausationID: &causationID, Data: response,
	}
	payload, err := natswire.Encode(validator, contractsv1.RegistrationResponseSchemaID, reply)
	if err != nil {
		logger.Error("encode registration response", "registration_id", request.ID, "error", err)
		return
	}
	replyMessage := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, replyMessage.Header)
	if err := connection.PublishMsg(replyMessage); err != nil {
		logger.Error("publish registration response", "registration_id", request.ID, "error", err)
	}
}

func register(ctx context.Context, registrar Registrar, adapterID string, wire registration) (registrationResponse, error) {
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
			TypeID:  devices.EntityTypeID(entity.Type),
			Support: append(devices.EntitySupport(nil), entity.Support...),
		}
	}
	accepted, err := registrar.Register(ctx, adapterID, domain)
	var rejected *devices.RegistrationRejectedError
	if errors.As(err, &rejected) {
		return registrationResponse{
			Status: "rejected",
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
		wireBinding.Entities[index] = entityBinding{Key: entity.Key, EntityID: string(entity.EntityID)}
	}
	return registrationResponse{Status: "accepted", Binding: &wireBinding}, nil
}

func newReplyID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return "rep_" + id.String(), nil
}

func copyStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
