//go:build integration

package nats //nolint:testpackage // Tests exercise package-private consumer provisioning.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// First provisioning must create the exact delivery, acknowledgement, and filter settings.
func TestProvisionDeviceFactConsumerCreatesExactConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	js := startDeviceFactServer(t)

	consumer, err := ProvisionDeviceFactConsumer(ctx, js, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	config := consumer.CachedInfo().Config
	switch {
	case config.Name != DeviceFactConsumerName:
		t.Fatalf("consumer name = %q, want %q", config.Name, DeviceFactConsumerName)
	case config.Durable != DeviceFactConsumerName:
		t.Fatalf("consumer durable = %q, want %q", config.Durable, DeviceFactConsumerName)
	case config.DeliverPolicy != jetstream.DeliverNewPolicy:
		t.Fatalf("deliver policy = %v, want new", config.DeliverPolicy)
	case config.AckPolicy != jetstream.AckExplicitPolicy:
		t.Fatalf("ack policy = %v, want explicit", config.AckPolicy)
	case config.AckWait != DeviceFactConsumerAckWait:
		t.Fatalf("ack wait = %s, want %s", config.AckWait, DeviceFactConsumerAckWait)
	case config.MaxDeliver != DeviceFactConsumerUnlimitedRedelivery:
		t.Fatalf("max deliver = %d, want unlimited redelivery", config.MaxDeliver)
	case config.MaxAckPending != DeviceFactConsumerMaxAckPending:
		t.Fatalf("max ack pending = %d, want %d", config.MaxAckPending, DeviceFactConsumerMaxAckPending)
	case config.ReplayPolicy != jetstream.ReplayInstantPolicy:
		t.Fatalf("replay policy = %v, want instant", config.ReplayPolicy)
	case config.FilterSubject != natswire.DeviceFactWildcard():
		t.Fatalf("filter subject = %q, want %q", config.FilterSubject, natswire.DeviceFactWildcard())
	}
	if validationErr := ValidateDeviceFactConsumer(ctx, js, testDeviceFactStreamName); validationErr != nil {
		t.Fatalf("validate provisioned consumer: %v", validationErr)
	}
}

// Repeated provisioning must reuse the existing durable consumer unchanged.
func TestProvisionDeviceFactConsumerIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	js := startDeviceFactServer(t)
	first, err := ProvisionDeviceFactConsumer(ctx, js, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProvisionDeviceFactConsumer(ctx, js, testDeviceFactStreamName)
	if err != nil {
		t.Fatalf("second provisioning: %v", err)
	}
	if first.CachedInfo().Created != second.CachedInfo().Created {
		t.Fatal("second provisioning replaced the durable consumer")
	}
	if second.CachedInfo().Config.DeliverPolicy != jetstream.DeliverNewPolicy {
		t.Fatal("second provisioning changed the delivery policy")
	}
	stream, err := js.Stream(ctx, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Consumers != 1 {
		t.Fatalf("stream holds %d consumers, want exactly one", info.State.Consumers)
	}
}

// The devices-owned Fact stream must exist before consumer provisioning.
func TestProvisionDeviceFactConsumerRejectsUnknownStream(t *testing.T) {
	t.Parallel()
	_, err := ProvisionDeviceFactConsumer(
		context.Background(), startDeviceFactServer(t), "HEARTH_ABSENT_STREAM_V1",
	)
	if err == nil || !strings.Contains(err.Error(), "get device fact stream") {
		t.Fatalf("provisioning error = %v, want a missing-stream failure", err)
	}
}

// Provisioning must reject existing consumers with mismatched delivery semantics.
func TestProvisionDeviceFactConsumerRejectsMismatchedExistingConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		want   string
		mutate func(*jetstream.ConsumerConfig)
	}{
		{"deliver policy", "does not match", func(config *jetstream.ConsumerConfig) {
			config.DeliverPolicy = jetstream.DeliverAllPolicy
		}},
		{"ack policy", "does not match", func(config *jetstream.ConsumerConfig) {
			config.AckPolicy = jetstream.AckNonePolicy
			config.MaxAckPending = 0
		}},
		{"ack wait", "does not match", func(config *jetstream.ConsumerConfig) {
			config.AckWait = testDuplicateWindow
		}},
		{"max deliver", "does not match", func(config *jetstream.ConsumerConfig) { config.MaxDeliver = 1 }},
		{"max ack pending", "does not match", func(config *jetstream.ConsumerConfig) { config.MaxAckPending = 5 }},
		{"replay policy", "does not match", func(config *jetstream.ConsumerConfig) {
			config.ReplayPolicy = jetstream.ReplayOriginalPolicy
		}},
		{"filter subject", "does not match", func(config *jetstream.ConsumerConfig) {
			family, err := natswire.DeviceFactFamilyWildcard(natswire.DeviceFactFamilyObservation)
			if err != nil {
				panic(err)
			}
			config.FilterSubject = family
		}},
		{"headers only", "headers_only", func(config *jetstream.ConsumerConfig) {
			config.HeadersOnly = true
		}},
		// DeliverSubject creates a push consumer, so the broker handle itself
		// rejects it before its configuration is inspected.
		{"deliver subject", "not a pull consumer", func(config *jetstream.ConsumerConfig) {
			config.DeliverSubject = "probe.deliver"
		}},
		{"deliver group", "deliver_group", func(config *jetstream.ConsumerConfig) {
			config.DeliverGroup = "probe-group"
		}},
		{"filter subjects", "filter_subjects", func(config *jetstream.ConsumerConfig) {
			config.FilterSubject = ""
			config.FilterSubjects = []string{natswire.DeviceFactWildcard()}
		}},
		{"backoff", "backoff", func(config *jetstream.ConsumerConfig) {
			config.BackOff = []time.Duration{time.Second}
		}},
		{"max request batch", "max_batch", func(config *jetstream.ConsumerConfig) {
			config.MaxRequestBatch = 10
		}},
		{"max request expires", "max_expires", func(config *jetstream.ConsumerConfig) {
			config.MaxRequestExpires = time.Second
		}},
		{"max request max bytes", "max_bytes", func(config *jetstream.ConsumerConfig) {
			config.MaxRequestMaxBytes = 100
		}},
		{"inactive threshold", "inactive_threshold", func(config *jetstream.ConsumerConfig) {
			config.InactiveThreshold = time.Minute
		}},
		{"memory storage", "mem_storage", func(config *jetstream.ConsumerConfig) {
			config.MemoryStorage = true
		}},
		{"pause until", "pause_until", func(config *jetstream.ConsumerConfig) {
			pauseUntil := time.Now().Add(time.Minute)
			config.PauseUntil = &pauseUntil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			js := startDeviceFactServer(t)
			stream, err := js.Stream(ctx, testDeviceFactStreamName)
			if err != nil {
				t.Fatal(err)
			}
			config := deviceFactConsumerConfig()
			test.mutate(&config)
			if _, createErr := stream.CreateConsumer(ctx, config); createErr != nil {
				t.Fatalf("create mismatched consumer: %v", createErr)
			}
			_, err = ProvisionDeviceFactConsumer(ctx, js, testDeviceFactStreamName)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("provisioning error = %v, want a %q rejection", err, test.want)
			}
		})
	}
}
