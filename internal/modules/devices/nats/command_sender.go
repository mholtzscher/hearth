package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type CommandSender struct {
	connection *natsgo.Conn
	validator  *contractsv1.Validator
}

func NewCommandSender(connection *natsgo.Conn, validator *contractsv1.Validator) *CommandSender {
	return &CommandSender{connection: connection, validator: validator}
}

func (sender *CommandSender) Send(
	ctx context.Context,
	adapterID string,
	request devices.CommandRequest,
) (devices.CommandAcceptance, error) {
	if sender == nil || sender.connection == nil || sender.validator == nil {
		return devices.CommandAcceptance{}, errors.New("command client is not initialized")
	}
	subject, err := natswire.CommandSubject(adapterID, "", string(request.EntityID), string(request.OperationName))
	if err != nil {
		return devices.CommandAcceptance{}, err
	}
	envelope := natswire.Envelope[command]{
		ID: string(request.ID), Schema: contractsv1.CommandRequestSchemaID,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: string(request.CorrelationID),
		Data: command{
			EntityID: string(request.EntityID), OperationName: string(request.OperationName),
			Parameters: append([]byte(nil), request.Parameters...),
			Deadline:   request.Deadline.UTC().Format(time.RFC3339Nano),
		},
	}
	payload, err := natswire.Encode(sender.validator, contractsv1.CommandRequestSchemaID, envelope)
	if err != nil {
		return devices.CommandAcceptance{}, fmt.Errorf("encode command request: %w", err)
	}
	message := &natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, message.Header)
	reply, err := sender.connection.RequestMsgWithContext(ctx, message)
	if err != nil {
		if commandUnavailable(err) {
			return devices.CommandAcceptance{}, fmt.Errorf(
				"%w: command adapter unavailable: %w",
				devices.ErrAdapterUnavailable,
				err,
			)
		}
		return devices.CommandAcceptance{}, fmt.Errorf("request command: %w", err)
	}
	response, err := natswire.Decode[commandResponse](sender.validator, contractsv1.CommandResponseSchemaID, reply.Data)
	if err != nil {
		return devices.CommandAcceptance{}, fmt.Errorf("decode command response: %w", err)
	}
	if response.CausationID == nil || *response.CausationID != string(request.ID) {
		return devices.CommandAcceptance{}, errors.New("command response causation ID does not match request")
	}
	if response.CorrelationID != string(request.CorrelationID) {
		return devices.CommandAcceptance{}, errors.New("command response correlation ID does not match request")
	}
	if response.Data.CommandID != string(request.ID) {
		return devices.CommandAcceptance{}, errors.New("command response command ID does not match request")
	}
	return devices.CommandAcceptance{Accepted: response.Data.Status == statusAccepted}, nil
}

func commandUnavailable(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsgo.ErrNoResponders) ||
		errors.Is(err, natsgo.ErrTimeout) || errors.Is(err, natsgo.ErrDisconnected) ||
		errors.Is(err, natsgo.ErrConnectionClosed) || errors.Is(err, natsgo.ErrConnectionDraining)
}
