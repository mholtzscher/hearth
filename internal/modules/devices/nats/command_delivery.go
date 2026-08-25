package nats

import (
	"context"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type CommandDelivery struct {
	client *platformnats.CommandClient
}

func NewCommandDelivery(client *platformnats.CommandClient) *CommandDelivery {
	return &CommandDelivery{client: client}
}

func (delivery *CommandDelivery) Deliver(
	ctx context.Context,
	adapterID string,
	dispatch devices.CommandDispatch,
) (devices.CommandAcceptance, error) {
	if delivery == nil || delivery.client == nil {
		return devices.CommandAcceptance{}, errors.New("Command delivery is not initialized")
	}
	acceptance, err := delivery.client.Send(ctx, adapterID, platformnats.CommandRequest{
		ID: string(dispatch.ID), CorrelationID: string(dispatch.CorrelationID),
		EntityID: string(dispatch.EntityID), OperationName: string(dispatch.OperationName),
		Parameters: append([]byte(nil), dispatch.Parameters...), Deadline: dispatch.Deadline,
	})
	if errors.Is(err, platformnats.ErrCommandUnavailable) {
		return devices.CommandAcceptance{}, fmt.Errorf("%w: %v", devices.ErrAdapterUnavailable, err)
	}
	if err != nil {
		return devices.CommandAcceptance{}, err
	}
	return devices.CommandAcceptance{Accepted: acceptance.Accepted}, nil
}
