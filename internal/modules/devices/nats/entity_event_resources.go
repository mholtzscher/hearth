package nats

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// Entity Event resources are separate from Observation resources on purpose:
// button and gesture history must never be mistaken for State, and the stream
// is the only transport for reports that arrive while Core is offline.
const (
	EntityEventStreamName             = "HEARTH_ENTITY_EVENTS_V1"
	EntityEventConsumerName           = "hearthd-entity-events-v1"
	EntityEventStreamMaxAge           = 7 * 24 * time.Hour
	EntityEventStreamMaxBytes   int64 = 1 << 30
	EntityEventStreamMaxMsgSize       = 4 * 1024
	EntityEventAckWait                = 30 * time.Second
)

func ProvisionEntityEventResources(ctx context.Context, js jetstream.JetStream) (jetstream.Consumer, error) {
	stream, err := js.Stream(ctx, EntityEventStreamName)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		stream, err = js.CreateStream(ctx, entityEventStreamConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("provision entity event stream: %w", err)
	}
	if validationErr := validateEntityEventStream(ctx, stream); validationErr != nil {
		return nil, validationErr
	}

	consumer, err := stream.Consumer(ctx, EntityEventConsumerName)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		consumer, err = stream.CreateConsumer(ctx, entityEventConsumerConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("provision entity event consumer: %w", err)
	}
	if validationErr := validateEntityEventConsumer(ctx, consumer); validationErr != nil {
		return nil, validationErr
	}
	return consumer, nil
}

func ValidateEntityEventResources(ctx context.Context, js jetstream.JetStream) error {
	stream, err := js.Stream(ctx, EntityEventStreamName)
	if err != nil {
		return fmt.Errorf("get entity event stream: %w", err)
	}
	if validationErr := validateEntityEventStream(ctx, stream); validationErr != nil {
		return validationErr
	}
	consumer, err := stream.Consumer(ctx, EntityEventConsumerName)
	if err != nil {
		return fmt.Errorf("get entity event consumer: %w", err)
	}
	return validateEntityEventConsumer(ctx, consumer)
}

func entityEventStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              EntityEventStreamName,
		Subjects:          []string{natswire.EntityEventWildcard()},
		Storage:           jetstream.FileStorage,
		Retention:         jetstream.LimitsPolicy,
		MaxMsgs:           -1,
		MaxAge:            EntityEventStreamMaxAge,
		MaxBytes:          EntityEventStreamMaxBytes,
		MaxMsgsPerSubject: -1,
		MaxMsgSize:        EntityEventStreamMaxMsgSize,
		Discard:           jetstream.DiscardOld,
		NoAck:             false,
	}
}

func entityEventConsumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Name:          EntityEventConsumerName,
		Durable:       EntityEventConsumerName,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       EntityEventAckWait,
		MaxDeliver:    -1,
		MaxAckPending: 1,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		FilterSubject: natswire.EntityEventWildcard(),
	}
}

func validateEntityEventStream(ctx context.Context, stream jetstream.Stream) error {
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect entity event stream: %w", err)
	}
	config := info.Config
	if config.Name != EntityEventStreamName ||
		!slices.Equal(config.Subjects, []string{natswire.EntityEventWildcard()}) ||
		config.Storage != jetstream.FileStorage ||
		config.Retention != jetstream.LimitsPolicy ||
		config.MaxMsgs != -1 ||
		config.MaxAge != EntityEventStreamMaxAge ||
		config.MaxBytes != EntityEventStreamMaxBytes ||
		config.MaxMsgsPerSubject != -1 ||
		config.MaxMsgSize != EntityEventStreamMaxMsgSize ||
		config.Discard != jetstream.DiscardOld ||
		config.NoAck {
		return errors.New("entity event stream configuration does not match required v1 settings")
	}
	return nil
}

func validateEntityEventConsumer(ctx context.Context, consumer jetstream.Consumer) error {
	info, err := consumer.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect entity event consumer: %w", err)
	}
	config := info.Config
	if config.Name != EntityEventConsumerName ||
		config.Durable != EntityEventConsumerName ||
		config.DeliverPolicy != jetstream.DeliverAllPolicy ||
		config.AckPolicy != jetstream.AckExplicitPolicy ||
		config.AckWait != EntityEventAckWait ||
		config.MaxDeliver != -1 ||
		config.MaxAckPending != 1 ||
		config.ReplayPolicy != jetstream.ReplayInstantPolicy ||
		config.FilterSubject != natswire.EntityEventWildcard() {
		return errors.New("entity event consumer configuration does not match required v1 settings")
	}
	return nil
}
