package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/propagation"
)

var ErrCommandUnavailable = errors.New("command adapter unavailable")

type CommandRequest struct {
	ID            string
	CorrelationID string
	EntityID      string
	OperationName string
	Parameters    []byte
	Deadline      time.Time
}

type CommandAcceptance struct {
	Accepted bool
}

type CommandClient struct {
	connection *natsgo.Conn
	validator  *contractsv1.Validator
	propagator propagation.TextMapPropagator
}

func NewCommandClient(connection *natsgo.Conn, validator *contractsv1.Validator) *CommandClient {
	return &CommandClient{connection: connection, validator: validator, propagator: propagation.TraceContext{}}
}

func (client *CommandClient) Send(ctx context.Context, adapterID string, request CommandRequest) (CommandAcceptance, error) {
	if client == nil || client.connection == nil || client.validator == nil {
		return CommandAcceptance{}, errors.New("command client is not initialized")
	}
	subject, err := CommandSubject(adapterID, request.EntityID, request.OperationName)
	if err != nil {
		return CommandAcceptance{}, err
	}
	envelope := Envelope[Command]{
		ID: request.ID, Schema: contractsv1.CommandRequestSchemaID,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: request.CorrelationID,
		Data: Command{
			EntityID: request.EntityID, OperationName: request.OperationName,
			Parameters: append([]byte(nil), request.Parameters...),
			Deadline:   request.Deadline.UTC().Format(time.RFC3339Nano),
		},
	}
	payload, err := Encode(client.validator, contractsv1.CommandRequestSchemaID, envelope)
	if err != nil {
		return CommandAcceptance{}, fmt.Errorf("encode command request: %w", err)
	}
	message := &natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload}
	client.propagator.Inject(ctx, HeaderCarrier(message.Header))
	reply, err := client.connection.RequestMsgWithContext(ctx, message)
	if err != nil {
		if commandUnavailable(err) {
			return CommandAcceptance{}, fmt.Errorf("%w: %v", ErrCommandUnavailable, err)
		}
		return CommandAcceptance{}, fmt.Errorf("request command: %w", err)
	}
	response, err := Decode[CommandResponse](client.validator, contractsv1.CommandResponseSchemaID, reply.Data)
	if err != nil {
		return CommandAcceptance{}, fmt.Errorf("decode command response: %w", err)
	}
	if response.CausationID == nil || *response.CausationID != request.ID {
		return CommandAcceptance{}, errors.New("command response causation ID does not match request")
	}
	if response.CorrelationID != request.CorrelationID {
		return CommandAcceptance{}, errors.New("command response correlation ID does not match request")
	}
	if response.Data.CommandID != request.ID {
		return CommandAcceptance{}, errors.New("command response command ID does not match request")
	}
	return CommandAcceptance{Accepted: response.Data.Status == "accepted"}, nil
}

func commandUnavailable(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsgo.ErrNoResponders) ||
		errors.Is(err, natsgo.ErrTimeout) || errors.Is(err, natsgo.ErrDisconnected) ||
		errors.Is(err, natsgo.ErrConnectionClosed) || errors.Is(err, natsgo.ErrConnectionDraining)
}
