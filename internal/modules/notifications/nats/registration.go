package nats

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
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
	subscription *natsgo.Subscription
	connection   *natsgo.Conn
	validator    *contractsv1.Validator
	registrar    Registrar
	logger       *slog.Logger
	mutex        sync.Mutex
	closed       bool
	active       sync.WaitGroup
}

func StartRegistrationServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	registrar Registrar,
	logger *slog.Logger,
) (*RegistrationServer, error) {
	if connection == nil || validator == nil || registrar == nil {
		return nil, notifications.ErrInvalidInput
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &RegistrationServer{connection: connection, validator: validator, registrar: registrar, logger: logger}
	sub, err := connection.Subscribe(natswire.NotificationRegistrationWildcard(), s.receive)
	if err != nil {
		return nil, err
	}
	s.subscription = sub
	ctx, cancel := context.WithTimeout(context.Background(), notifications.PersistenceTimeout)
	defer cancel()
	if err = connection.FlushWithContext(ctx); err != nil {
		_ = sub.Unsubscribe()
		return nil, err
	}
	return s, nil
}
func (s *RegistrationServer) Close(ctx context.Context) error {
	s.mutex.Lock()
	if !s.closed {
		s.closed = true
		_ = s.subscription.Unsubscribe()
	}
	s.mutex.Unlock()
	done := make(chan struct{})
	go func() { s.active.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *RegistrationServer) receive(message *natsgo.Msg) {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return
	}
	s.active.Add(1)
	s.mutex.Unlock()
	defer s.active.Done()
	if message.Reply == "" {
		return
	}
	route, err := natswire.ParseNotificationRegistrationSubject(message.Subject)
	if err != nil {
		return
	}
	request, err := natswire.Decode[registrationRequest](
		s.validator,
		contractsv1.NotificationRegistrationRequestSchemaID,
		message.Data,
	)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(
		natswire.ExtractTrace(context.Background(), message.Header),
		notifications.PersistenceTimeout,
	)
	defer cancel()
	destination, err := s.registrar.Register(
		ctx,
		notifications.Registration{
			AdapterID: route.AdapterID,
			RuntimeID: route.RuntimeID,
			Key:       request.Data.Key,
			Name:      request.Data.Name,
		},
	)
	data := registrationResponse{Status: "accepted", DestinationID: string(destination.ID)}
	switch {
	case errors.Is(err, notifications.ErrStaleRuntime):
		data = registrationResponse{Status: "rejected", ReasonCode: "stale_runtime"}
	case errors.Is(err, notifications.ErrInvalidInput):
		data = registrationResponse{Status: "rejected", ReasonCode: "invalid_registration"}
	case err != nil:
		s.logger.ErrorContext(ctx, "persist notification registration")
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		return
	}
	response := natswire.Envelope[registrationResponse]{
		ID:            "rep_" + id.String(),
		Schema:        contractsv1.NotificationRegistrationResponseSchemaID,
		EmittedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		CorrelationID: request.CorrelationID,
		CausationID:   &request.ID,
		Data:          data,
	}
	payload, err := natswire.Encode(s.validator, contractsv1.NotificationRegistrationResponseSchemaID, response)
	if err != nil {
		return
	}
	reply := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, reply.Header)
	_ = s.connection.PublishMsg(reply)
}
