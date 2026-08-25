package nats

import natsgo "github.com/nats-io/nats.go"

func (server *RegistrationServer) Closed() <-chan struct{} {
	closed := make(chan struct{})
	if server == nil || server.subscription == nil {
		close(closed)
		return closed
	}
	status := server.subscription.StatusChanged(natsgo.SubscriptionClosed)
	go func() {
		defer close(closed)
		<-status
	}()
	return closed
}
