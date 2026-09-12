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
	// DeviceFactStreamName is the one JetStream stream that durably holds every
	// published Device Fact until its MaxAge or MaxBytes bound evicts it. Facts
	// are rebuilt from the transactional outbox, not from the stream, so the
	// stream is a delivery surface rather than Core's source of truth.
	DeviceFactStreamName = "HEARTH_DEVICE_FACTS_V1"
	// DeviceFactStreamMaxAge bounds how long a published fact stays readable.
	DeviceFactStreamMaxAge = 7 * 24 * time.Hour
	// DeviceFactStreamMaxBytes bounds the stream's on-disk size. LimitsPolicy
	// with DiscardOld evicts the oldest fact first when either bound is reached.
	DeviceFactStreamMaxBytes int64 = 1 << 30
	// DeviceFactStreamDuplicateWindow is the explicit broker-side window in
	// which a repeated Nats-Msg-Id is acknowledged as a duplicate instead of
	// stored again. The relay retries a publish whose PubAck or row delete was
	// lost, and within this window the broker collapses that retry. The window is
	// bounded, and the relay's retry span is not: a long broker outage or a
	// restart can outlast it, so a retry after the window is stored again and a
	// reader must stay idempotent on the stable fact identity.
	DeviceFactStreamDuplicateWindow = 2 * time.Hour
)

// ProvisionDeviceFactStream creates the Device Fact stream when it is absent and
// then validates the live configuration. It deliberately creates no consumer:
// every consumer is owned by the reader that needs it, so Core never pins an ack
// floor or a delivery policy for a reader it does not have.
func ProvisionDeviceFactStream(ctx context.Context, js jetstream.JetStream) error {
	stream, err := js.Stream(ctx, DeviceFactStreamName)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		stream, err = js.CreateStream(ctx, deviceFactStreamConfig())
	}
	if err != nil {
		return fmt.Errorf("provision device fact stream: %w", err)
	}
	return validateDeviceFactStream(ctx, stream)
}

// ValidateDeviceFactStream reports whether the live Device Fact stream already
// carries the required v1 configuration. It never creates or updates anything.
func ValidateDeviceFactStream(ctx context.Context, js jetstream.JetStream) error {
	stream, err := js.Stream(ctx, DeviceFactStreamName)
	if err != nil {
		return fmt.Errorf("get device fact stream: %w", err)
	}
	return validateDeviceFactStream(ctx, stream)
}

func deviceFactStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              DeviceFactStreamName,
		Subjects:          []string{natswire.DeviceFactWildcard()},
		Storage:           jetstream.FileStorage,
		Retention:         jetstream.LimitsPolicy,
		MaxMsgs:           -1,
		MaxAge:            DeviceFactStreamMaxAge,
		MaxBytes:          DeviceFactStreamMaxBytes,
		MaxMsgsPerSubject: -1,
		MaxMsgSize:        -1,
		Discard:           jetstream.DiscardOld,
		Duplicates:        DeviceFactStreamDuplicateWindow,
		NoAck:             false,
	}
}

func validateDeviceFactStream(ctx context.Context, stream jetstream.Stream) error {
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect device fact stream: %w", err)
	}
	config := info.Config
	// A subject transform is rejected outright: it would rewrite a canonical fact
	// subject before the broker stores it, while PublishMsg still returns a
	// successful PubAck for the original subject. The relay would then delete the
	// outbox row even though no consumer can ever read that fact under the
	// canonical subject it was published with.
	if config.Name != DeviceFactStreamName ||
		!slices.Equal(config.Subjects, []string{natswire.DeviceFactWildcard()}) ||
		config.SubjectTransform != nil ||
		config.Storage != jetstream.FileStorage ||
		config.Retention != jetstream.LimitsPolicy ||
		config.MaxMsgs != -1 ||
		config.MaxAge != DeviceFactStreamMaxAge ||
		config.MaxBytes != DeviceFactStreamMaxBytes ||
		config.MaxMsgsPerSubject != -1 ||
		config.MaxMsgSize != -1 ||
		config.Discard != jetstream.DiscardOld ||
		config.Duplicates != DeviceFactStreamDuplicateWindow ||
		config.NoAck {
		return errors.New(
			"device fact stream configuration does not match required v1 settings " +
				"(canonical subjects, no subject transform, exact bounded limits)",
		)
	}
	return nil
}
