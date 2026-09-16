// Package nats owns the automation Device Fact consumer, strict wire decoding,
// and message acknowledgements.
package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

const (
	// DeviceFactConsumerName identifies the single durable consumer for both Fact
	// families. Definition edits do not change its subscription.
	DeviceFactConsumerName = "hearthd-automation-device-facts-v1"
	// DeviceFactConsumerAckWait bounds how long the broker waits for one
	// admission disposition before redelivering the Fact.
	DeviceFactConsumerAckWait = 5 * time.Second
	// DeviceFactConsumerNakDelay is the delayed negative acknowledgement used for
	// a transient admission or storage failure, so a retry is not a hot loop.
	DeviceFactConsumerNakDelay = 1 * time.Second
	// DeviceFactConsumerMaxAckPending limits unacknowledged Facts, not concurrent Runs.
	DeviceFactConsumerMaxAckPending = 1
	// DeviceFactConsumerUnlimitedRedelivery keeps a transiently failing Fact
	// repairable for as long as the bounded stream retains it.
	DeviceFactConsumerUnlimitedRedelivery = -1
	// DeviceFactAdmissionTimeout sets an admission deadline shorter than AckWait
	// to allow a disposition before broker redelivery. It is an alias of the
	// authoritative automations.AdmissionTimeout, which also covers
	// Condition State snapshot reads; the duration is never repeated here.
	DeviceFactAdmissionTimeout = automations.AdmissionTimeout
)

// ProvisionDeviceFactConsumer creates or validates a durable consumer on the
// supplied devices-owned stream. New consumers start at the tail; existing ones
// resume their acknowledgement floor.
func ProvisionDeviceFactConsumer(
	ctx context.Context,
	js jetstream.JetStream,
	streamName string,
) (jetstream.Consumer, error) {
	if streamName == "" {
		return nil, errors.New("device fact stream name is required")
	}
	stream, err := js.Stream(ctx, streamName)
	if err != nil {
		return nil, fmt.Errorf("get device fact stream %q: %w", streamName, err)
	}
	consumer, err := stream.Consumer(ctx, DeviceFactConsumerName)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		consumer, err = stream.CreateConsumer(ctx, deviceFactConsumerConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("provision device fact consumer: %w", err)
	}
	if validationErr := validateDeviceFactConsumer(ctx, consumer); validationErr != nil {
		return nil, validationErr
	}
	return consumer, nil
}

// ValidateDeviceFactConsumer checks live configuration without changing it.
func ValidateDeviceFactConsumer(ctx context.Context, js jetstream.JetStream, streamName string) error {
	if streamName == "" {
		return errors.New("device fact stream name is required")
	}
	stream, err := js.Stream(ctx, streamName)
	if err != nil {
		return fmt.Errorf("get device fact stream %q: %w", streamName, err)
	}
	consumer, err := stream.Consumer(ctx, DeviceFactConsumerName)
	if err != nil {
		return fmt.Errorf("get device fact consumer: %w", err)
	}
	return validateDeviceFactConsumer(ctx, consumer)
}

func deviceFactConsumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Name:          DeviceFactConsumerName,
		Durable:       DeviceFactConsumerName,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       DeviceFactConsumerAckWait,
		MaxDeliver:    DeviceFactConsumerUnlimitedRedelivery,
		MaxAckPending: DeviceFactConsumerMaxAckPending,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		FilterSubject: natswire.DeviceFactWildcard(),
	}
}

func validateDeviceFactConsumer(ctx context.Context, consumer jetstream.Consumer) error {
	info, err := consumer.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect device fact consumer: %w", err)
	}
	return validateDeviceFactConsumerConfig(info.Config)
}

// validateDeviceFactConsumerConfig checks required settings and rejects delivery overrides.
func validateDeviceFactConsumerConfig(config jetstream.ConsumerConfig) error {
	if override := deviceFactConsumerDeliveryOverride(config); override != "" {
		return fmt.Errorf(
			"device fact consumer configuration carries the %q delivery override; "+
				"the required v1 consumer delivers every payload with exact redelivery",
			override,
		)
	}
	if config.Name != DeviceFactConsumerName ||
		config.Durable != DeviceFactConsumerName ||
		config.DeliverPolicy != jetstream.DeliverNewPolicy ||
		config.AckPolicy != jetstream.AckExplicitPolicy ||
		config.AckWait != DeviceFactConsumerAckWait ||
		config.MaxDeliver != DeviceFactConsumerUnlimitedRedelivery ||
		config.MaxAckPending != DeviceFactConsumerMaxAckPending ||
		config.ReplayPolicy != jetstream.ReplayInstantPolicy ||
		config.FilterSubject != natswire.DeviceFactWildcard() {
		return errors.New(
			"device fact consumer configuration does not match required v1 settings " +
				"(durable name, canonical filter, DeliverNew first creation, explicit ack, " +
				"exact AckWait and MaxAckPending, unlimited redelivery, instant replay)",
		)
	}
	return nil
}

// deviceFactConsumerDeliveryOverride names the first unexpected delivery
// override, ignoring broker-populated Metadata and MaxWaiting.
func deviceFactConsumerDeliveryOverride(config jetstream.ConsumerConfig) string {
	switch {
	case config.HeadersOnly:
		return "headers_only"
	case config.DeliverSubject != "":
		return "deliver_subject"
	case config.DeliverGroup != "":
		return "deliver_group"
	case len(config.FilterSubjects) != 0:
		return "filter_subjects"
	case len(config.BackOff) != 0:
		return "backoff"
	case config.RateLimit != 0:
		return "rate_limit_bps"
	case config.SampleFrequency != "":
		return "sample_freq"
	case config.MaxRequestBatch != 0:
		return "max_batch"
	case config.MaxRequestExpires != 0:
		return "max_expires"
	case config.MaxRequestMaxBytes != 0:
		return "max_bytes"
	case config.InactiveThreshold != 0:
		return "inactive_threshold"
	case config.MemoryStorage:
		return "mem_storage"
	case config.FlowControl:
		return "flow_control"
	case config.IdleHeartbeat != 0:
		return "idle_heartbeat"
	case config.OptStartSeq != 0:
		return "opt_start_seq"
	case config.OptStartTime != nil:
		return "opt_start_time"
	case config.PauseUntil != nil:
		return "pause_until"
	case config.PriorityPolicy != jetstream.PriorityPolicyNone:
		return "priority_policy"
	case config.PinnedTTL != 0:
		return "priority_timeout"
	case len(config.PriorityGroups) != 0:
		return "priority_groups"
	}
	return ""
}
