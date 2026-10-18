package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const (
	AdapterHealthConsumerName = "hearthd-device-availability-v1"
	AdapterHealthAckWait      = 30 * time.Second
)

// ProvisionAdapterHealthConsumer takes a stream identity from assembly, not the producer transport.
func ProvisionAdapterHealthConsumer(
	ctx context.Context,
	js jetstream.JetStream,
	streamName string,
) (jetstream.Consumer, error) {
	stream, err := js.Stream(ctx, streamName)
	if err != nil {
		return nil, fmt.Errorf("get Adapter health stream: %w", err)
	}
	consumer, err := stream.Consumer(ctx, AdapterHealthConsumerName)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		consumer, err = stream.CreateConsumer(ctx, adapterHealthConsumerConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("provision Adapter health consumer: %w", err)
	}
	if err = validateAdapterHealthConsumer(ctx, consumer); err != nil {
		return nil, err
	}
	return consumer, nil
}

func ValidateAdapterHealthConsumer(ctx context.Context, js jetstream.JetStream, streamName string) error {
	stream, err := js.Stream(ctx, streamName)
	if err != nil {
		return err
	}
	consumer, err := stream.Consumer(ctx, AdapterHealthConsumerName)
	if err != nil {
		return err
	}
	return validateAdapterHealthConsumer(ctx, consumer)
}

func adapterHealthConsumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Name: AdapterHealthConsumerName, Durable: AdapterHealthConsumerName,
		DeliverPolicy: jetstream.DeliverAllPolicy, AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: AdapterHealthAckWait, MaxDeliver: -1, MaxAckPending: 1,
		ReplayPolicy: jetstream.ReplayInstantPolicy, FilterSubject: natswire.AdapterHealthWildcard(),
	}
}

func validateAdapterHealthConsumer(ctx context.Context, consumer jetstream.Consumer) error {
	info, err := consumer.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect Adapter health consumer: %w", err)
	}
	c := info.Config
	if c.Name != AdapterHealthConsumerName || c.Durable != AdapterHealthConsumerName ||
		c.DeliverPolicy != jetstream.DeliverAllPolicy || c.AckPolicy != jetstream.AckExplicitPolicy ||
		c.AckWait != AdapterHealthAckWait || c.MaxDeliver != -1 || c.MaxAckPending != 1 ||
		c.ReplayPolicy != jetstream.ReplayInstantPolicy || c.FilterSubject != natswire.AdapterHealthWildcard() ||
		len(c.FilterSubjects) != 0 || len(c.BackOff) != 0 || c.HeadersOnly || c.InactiveThreshold != 0 {
		return errors.New("adapter health consumer configuration does not match required v1 settings")
	}
	return nil
}
