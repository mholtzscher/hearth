// Package nats owns the automations module's Device Fact transport: its one
// durable JetStream consumer, strict wire decoding for both fact families, ack
// disposition, and mapping into automation-owned admission input. It never
// executes a Command and never reads another module's tables.
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
	// DeviceFactConsumerName is the one named durable consumer the automations
	// module owns. One consumer reads both fact families; definition edits never
	// create, recreate, or filter a consumer per Trigger.
	DeviceFactConsumerName = "hearthd-automation-device-facts-v1"
	// DeviceFactConsumerAckWait bounds how long the broker waits for one
	// admission disposition before redelivering the Fact.
	DeviceFactConsumerAckWait = 5 * time.Second
	// DeviceFactConsumerNakDelay is the delayed negative acknowledgement used for
	// a transient admission or storage failure, so a retry is not a hot loop.
	DeviceFactConsumerNakDelay = 1 * time.Second
	// DeviceFactConsumerMaxAckPending keeps exactly one Fact in flight, so one
	// slow admission cannot reorder or overlap household actions.
	DeviceFactConsumerMaxAckPending = 1
	// DeviceFactConsumerUnlimitedRedelivery keeps a transiently failing Fact
	// repairable for as long as the bounded stream retains it.
	DeviceFactConsumerUnlimitedRedelivery = -1
	// DeviceFactAdmissionTimeout bounds one synchronous admission call so a live
	// callback either commits before AckWait or negatively acknowledges before the
	// broker creates concurrent delivery.
	DeviceFactAdmissionTimeout = 2 * time.Second
)

// ProvisionDeviceFactConsumer opens or creates the automations-owned durable
// Device Fact consumer on the supplied Device Fact stream and validates its live
// configuration. The stream is provisioned and owned by devices; the automations
// transport receives its name rather than importing the devices transport to
// learn a constant.
//
// A consumer created for the first time starts at the then-current stream tail
// (DeliverNewPolicy). A consumer that already exists is reused exactly as it is,
// so it resumes from its own durable acknowledgement floor; an existing consumer
// whose live configuration does not match the required settings, or that carries
// a delivery override such as HeadersOnly, fails provisioning instead of
// silently changing delivery semantics.
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

// ValidateDeviceFactConsumer reports whether the live durable Device Fact
// consumer already carries the required configuration. It never creates or
// updates anything, so readiness can re-check the exact broker resources without
// changing delivery policy.
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

// validateDeviceFactConsumerConfig reports whether one live consumer
// configuration carries the required durable settings and none of the delivery
// overrides that would change what the consumer receives. It is a pure predicate
// over the live config so a directly supplied configuration can pin the exact
// rejection without a broker.
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

// deviceFactConsumerDeliveryOverride names the first delivery-affecting field a
// live consumer carries away from the required consumer's absent default, or the
// empty string when it carries none. The required consumer is a plain
// payload-delivering pull consumer, so each of these fields changes what it
// receives or how it redelivers even when every required field still matches:
// HeadersOnly drops the payload, FilterSubjects widens the filter, DeliverSubject
// and DeliverGroup turn it into a push consumer, BackOff replaces AckWait,
// PauseUntil and InactiveThreshold stop or delete it, and the request limits
// override pull batching. Broker-populated values are deliberately excluded:
// Metadata carries server keys, and MaxWaiting inherits a server default that
// MaxAckPending already bounds.
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
