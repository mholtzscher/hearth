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

const (
	ObservationStreamName           = "HEARTH_OBSERVATIONS_V1"
	ObservationConsumerName         = "hearthd-state-runtime-v1"
	ObservationStreamMaxAge         = 7 * 24 * time.Hour
	ObservationStreamMaxBytes int64 = 1 << 30
	ObservationAckWait              = 30 * time.Second
)

func ProvisionObservationResources(ctx context.Context, js jetstream.JetStream) (jetstream.Consumer, error) {
	stream, err := js.Stream(ctx, ObservationStreamName)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		stream, err = js.CreateStream(ctx, observationStreamConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("provision observation stream: %w", err)
	}
	if validationErr := validateObservationStream(ctx, stream); validationErr != nil {
		return nil, validationErr
	}

	consumer, err := stream.Consumer(ctx, ObservationConsumerName)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		consumer, err = stream.CreateConsumer(ctx, observationConsumerConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("provision observation consumer: %w", err)
	}
	if validationErr := validateObservationConsumer(ctx, consumer); validationErr != nil {
		return nil, validationErr
	}
	return consumer, nil
}

func ValidateObservationResources(ctx context.Context, js jetstream.JetStream) error {
	stream, err := js.Stream(ctx, ObservationStreamName)
	if err != nil {
		return fmt.Errorf("get observation stream: %w", err)
	}
	if validationErr := validateObservationStream(ctx, stream); validationErr != nil {
		return validationErr
	}
	consumer, err := stream.Consumer(ctx, ObservationConsumerName)
	if err != nil {
		return fmt.Errorf("get observation consumer: %w", err)
	}
	return validateObservationConsumer(ctx, consumer)
}

func observationStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              ObservationStreamName,
		Subjects:          []string{natswire.ObservationWildcard()},
		Storage:           jetstream.FileStorage,
		Retention:         jetstream.LimitsPolicy,
		MaxMsgs:           -1,
		MaxAge:            ObservationStreamMaxAge,
		MaxBytes:          ObservationStreamMaxBytes,
		MaxMsgsPerSubject: -1,
		MaxMsgSize:        -1,
		Discard:           jetstream.DiscardOld,
		NoAck:             false,
	}
}

func observationConsumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Name:          ObservationConsumerName,
		Durable:       ObservationConsumerName,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       ObservationAckWait,
		MaxDeliver:    -1,
		MaxAckPending: 1,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		FilterSubject: natswire.ObservationWildcard(),
	}
}

func validateObservationStream(ctx context.Context, stream jetstream.Stream) error {
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect observation stream: %w", err)
	}
	config := info.Config
	if config.Name != ObservationStreamName ||
		!slices.Equal(config.Subjects, []string{natswire.ObservationWildcard()}) ||
		config.Storage != jetstream.FileStorage ||
		config.Retention != jetstream.LimitsPolicy ||
		config.MaxMsgs != -1 ||
		config.MaxAge != ObservationStreamMaxAge ||
		config.MaxBytes != ObservationStreamMaxBytes ||
		config.MaxMsgsPerSubject != -1 ||
		config.MaxMsgSize != -1 ||
		config.Discard != jetstream.DiscardOld ||
		config.NoAck {
		return errors.New("observation stream configuration does not match required v1 settings")
	}
	return nil
}

func validateObservationConsumer(ctx context.Context, consumer jetstream.Consumer) error {
	info, err := consumer.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect observation consumer: %w", err)
	}
	config := info.Config
	if config.Name != ObservationConsumerName ||
		config.Durable != ObservationConsumerName ||
		config.DeliverPolicy != jetstream.DeliverAllPolicy ||
		config.AckPolicy != jetstream.AckExplicitPolicy ||
		config.AckWait != ObservationAckWait ||
		config.MaxDeliver != -1 ||
		config.MaxAckPending != 1 ||
		config.ReplayPolicy != jetstream.ReplayInstantPolicy ||
		config.FilterSubject != natswire.ObservationWildcard() {
		return errors.New("observation consumer configuration does not match required v1 settings")
	}
	return nil
}
