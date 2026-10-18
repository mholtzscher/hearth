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
	HealthStreamName            = "HEARTH_ADAPTER_HEALTH_V1"
	HealthMaxMessageSize        = 16 * 1024
	HealthStreamMaxBytes  int64 = 1 << 30
	HealthDuplicateWindow       = 2 * time.Minute
)

// ProvisionHealthStream creates missing resources and rejects incompatible ones.
func ProvisionHealthStream(ctx context.Context, js jetstream.JetStream) error {
	stream, err := js.Stream(ctx, HealthStreamName)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		stream, err = js.CreateStream(ctx, healthStreamConfig())
	}
	if err != nil {
		return fmt.Errorf("provision Adapter health stream: %w", err)
	}
	return validateHealthStream(ctx, stream)
}

func ValidateHealthStream(ctx context.Context, js jetstream.JetStream) error {
	stream, err := js.Stream(ctx, HealthStreamName)
	if err != nil {
		return fmt.Errorf("get Adapter health stream: %w", err)
	}
	return validateHealthStream(ctx, stream)
}

func healthStreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name: HealthStreamName, Subjects: []string{natswire.AdapterHealthWildcard()},
		Storage: jetstream.FileStorage, Retention: jetstream.WorkQueuePolicy,
		MaxMsgs: -1, MaxAge: 0, MaxBytes: HealthStreamMaxBytes,
		MaxMsgsPerSubject: -1, MaxMsgSize: HealthMaxMessageSize,
		Discard: jetstream.DiscardNew, Duplicates: HealthDuplicateWindow,
	}
}

func validateHealthStream(ctx context.Context, stream jetstream.Stream) error {
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect Adapter health stream: %w", err)
	}
	c := info.Config
	if c.Name != HealthStreamName || !slices.Equal(c.Subjects, []string{natswire.AdapterHealthWildcard()}) ||
		c.Storage != jetstream.FileStorage || c.Retention != jetstream.WorkQueuePolicy ||
		c.MaxMsgs != -1 || c.MaxAge != 0 || c.MaxBytes != HealthStreamMaxBytes ||
		c.MaxMsgsPerSubject != -1 || c.MaxMsgSize != HealthMaxMessageSize ||
		c.Discard != jetstream.DiscardNew || c.Duplicates != HealthDuplicateWindow ||
		c.NoAck || c.Sealed || c.SubjectTransform != nil || c.Mirror != nil || len(c.Sources) != 0 ||
		c.DiscardNewPerSubject || c.RePublish != nil || c.AllowMsgTTL {
		return errors.New("adapter health stream configuration does not match required v1 settings")
	}
	return nil
}
