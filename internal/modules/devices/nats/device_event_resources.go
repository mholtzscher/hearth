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

// Device Event resources are separate from Observation resources on purpose:
// button and gesture history must never be mistaken for State, and the stream
// is the only transport for reports that arrive while Core is offline.
const (
	DeviceEventStreamName             = "HEARTH_DEVICE_EVENTS_V1"
	DeviceEventConsumerName           = "hearthd-device-events-v1"
	DeviceEventStreamMaxAge           = 7 * 24 * time.Hour
	DeviceEventStreamMaxBytes   int64 = 1 << 30
	DeviceEventStreamMaxMsgSize       = 4 * 1024
	DeviceEventAckWait                = 30 * time.Second
)

func ProvisionDeviceEventResources(ctx context.Context, js jetstream.JetStream) (jetstream.Consumer, error) {
	stream, err := js.Stream(ctx, DeviceEventStreamName)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		stream, err = js.CreateStream(ctx, deviceEventStreamConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("provision device event stream: %w", err)
	}
	if validationErr := validateDeviceEventStream(ctx, stream); validationErr != nil {
		return nil, validationErr
	}

	consumer, err := stream.Consumer(ctx, DeviceEventConsumerName)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		consumer, err = stream.CreateConsumer(ctx, deviceEventConsumerConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("provision device event consumer: %w", err)
	}
	if validationErr := validateDeviceEventConsumer(ctx, consumer); validationErr != nil {
		return nil, validationErr
	}
	return consumer, nil
}

func ValidateDeviceEventResources(ctx context.Context, js jetstream.JetStream) error {
	stream, err := js.Stream(ctx, DeviceEventStreamName)
	if err != nil {
		return fmt.Errorf("get device event stream: %w", err)
	}
	if validationErr := validateDeviceEventStream(ctx, stream); validationErr != nil {
		return validationErr
	}
	consumer, err := stream.Consumer(ctx, DeviceEventConsumerName)
	if err != nil {
		return fmt.Errorf("get device event consumer: %w", err)
	}
	return validateDeviceEventConsumer(ctx, consumer)
}

func deviceEventStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              DeviceEventStreamName,
		Subjects:          []string{natswire.DeviceEventWildcard()},
		Storage:           jetstream.FileStorage,
		Retention:         jetstream.LimitsPolicy,
		MaxMsgs:           -1,
		MaxAge:            DeviceEventStreamMaxAge,
		MaxBytes:          DeviceEventStreamMaxBytes,
		MaxMsgsPerSubject: -1,
		MaxMsgSize:        DeviceEventStreamMaxMsgSize,
		Discard:           jetstream.DiscardOld,
		NoAck:             false,
	}
}

func deviceEventConsumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Name:          DeviceEventConsumerName,
		Durable:       DeviceEventConsumerName,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       DeviceEventAckWait,
		MaxDeliver:    -1,
		MaxAckPending: 1,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		FilterSubject: natswire.DeviceEventWildcard(),
	}
}

func validateDeviceEventStream(ctx context.Context, stream jetstream.Stream) error {
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect device event stream: %w", err)
	}
	config := info.Config
	if config.Name != DeviceEventStreamName ||
		!slices.Equal(config.Subjects, []string{natswire.DeviceEventWildcard()}) ||
		config.Storage != jetstream.FileStorage ||
		config.Retention != jetstream.LimitsPolicy ||
		config.MaxMsgs != -1 ||
		config.MaxAge != DeviceEventStreamMaxAge ||
		config.MaxBytes != DeviceEventStreamMaxBytes ||
		config.MaxMsgsPerSubject != -1 ||
		config.MaxMsgSize != DeviceEventStreamMaxMsgSize ||
		config.Discard != jetstream.DiscardOld ||
		config.NoAck {
		return errors.New("device event stream configuration does not match required v1 settings")
	}
	return nil
}

func validateDeviceEventConsumer(ctx context.Context, consumer jetstream.Consumer) error {
	info, err := consumer.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect device event consumer: %w", err)
	}
	config := info.Config
	if config.Name != DeviceEventConsumerName ||
		config.Durable != DeviceEventConsumerName ||
		config.DeliverPolicy != jetstream.DeliverAllPolicy ||
		config.AckPolicy != jetstream.AckExplicitPolicy ||
		config.AckWait != DeviceEventAckWait ||
		config.MaxDeliver != -1 ||
		config.MaxAckPending != 1 ||
		config.ReplayPolicy != jetstream.ReplayInstantPolicy ||
		config.FilterSubject != natswire.DeviceEventWildcard() {
		return errors.New("device event consumer configuration does not match required v1 settings")
	}
	return nil
}
