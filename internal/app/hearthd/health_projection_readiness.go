package hearthd

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go/jetstream"

	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// Derived delivery is a public readiness dependency, never the supervisor's
// source-readiness gate. Neither backlog nor projection lag means inactivity.
type healthProjectionReadiness struct {
	base      ReadinessChecker
	jetstream jetstream.JetStream
	relay     *adaptersnats.HealthRelay
	consumer  *platformnats.Consumer
}

func (readiness *healthProjectionReadiness) Check(ctx context.Context) error {
	if readiness.base == nil || readiness.jetstream == nil || readiness.relay == nil || readiness.consumer == nil {
		return errors.New("health delivery dependencies are not initialized")
	}
	if err := readiness.base.Check(ctx); err != nil {
		return err
	}
	if !readiness.relay.Active() || readiness.relay.Fault() != nil {
		return errors.New("health relay is unavailable")
	}
	if !readiness.consumer.Active() {
		return errors.New("health consumer is inactive; restart Core")
	}
	if err := adaptersnats.ValidateHealthStream(ctx, readiness.jetstream); err != nil {
		return err
	}
	return devicesnats.ValidateAdapterHealthConsumer(ctx, readiness.jetstream, adaptersnats.HealthStreamName)
}
