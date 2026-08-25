package nats

import natsgo "github.com/nats-io/nats.go"

func (server *RegistrationServer) Closed() <-chan natsgo.SubStatus {
	if server == nil || server.subscription == nil {
		closed := make(chan natsgo.SubStatus)
		close(closed)
		return closed
	}
	return server.subscription.StatusChanged(natsgo.SubscriptionClosed)
}
