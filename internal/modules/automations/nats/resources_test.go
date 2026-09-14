package nats //nolint:testpackage // Tests exercise package-private consumer provisioning.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// TestProvisionDeviceFactConsumerCreatesExactConfiguration protects the one
// durable consumer's required broker contract and fails if first creation uses
// any other delivery, acknowledgement, redelivery, or filter setting.
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

// TestProvisionDeviceFactConsumerIsIdempotent protects restart behavior and
// fails if a second provisioning pass recreates, mutates, or rejects its own
// existing durable consumer.
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

// TestProvisionDeviceFactConsumerRequiresStreamName protects the supplied-stream
// contract and fails if an empty stream name silently opens anything.
func TestProvisionDeviceFactConsumerRequiresStreamName(t *testing.T) {
	t.Parallel()
	if _, err := ProvisionDeviceFactConsumer(context.Background(), nil, ""); err == nil {
		t.Fatal("provisioning accepted an empty stream name")
	}
}

// TestProvisionDeviceFactConsumerRejectsUnknownStream protects startup ordering
// and fails if the automations consumer is created before devices provisions the
// Device Fact stream.
func TestProvisionDeviceFactConsumerRejectsUnknownStream(t *testing.T) {
	t.Parallel()
	_, err := ProvisionDeviceFactConsumer(
		context.Background(), startDeviceFactServer(t), "HEARTH_ABSENT_STREAM_V1",
	)
	if err == nil || !strings.Contains(err.Error(), "get device fact stream") {
		t.Fatalf("provisioning error = %v, want a missing-stream failure", err)
	}
}

// TestProvisionDeviceFactConsumerRejectsMismatchedExistingConfiguration protects
// exact broker resource validation and fails if an existing durable consumer
// with different delivery semantics or a delivery override is silently reused.
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

// TestValidateDeviceFactConsumerConfigRejectsEachField pins the exact
// configuration predicate without a broker, so a drift in any single required
// field is caught even if embedded provisioning could tolerate it.
func TestValidateDeviceFactConsumerConfigRejectsEachField(t *testing.T) {
	t.Parallel()
	base := deviceFactConsumerConfig()
	if err := validateDeviceFactConsumerConfig(base); err != nil {
		t.Fatalf("required configuration was rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*jetstream.ConsumerConfig)
	}{
		{"name", func(config *jetstream.ConsumerConfig) { config.Name = "other" }},
		{"durable", func(config *jetstream.ConsumerConfig) { config.Durable = "other" }},
		{"deliver policy", func(config *jetstream.ConsumerConfig) {
			config.DeliverPolicy = jetstream.DeliverAllPolicy
		}},
		{"ack policy", func(config *jetstream.ConsumerConfig) {
			config.AckPolicy = jetstream.AckAllPolicy
		}},
		{"ack wait", func(config *jetstream.ConsumerConfig) { config.AckWait = testDuplicateWindow }},
		{"max deliver", func(config *jetstream.ConsumerConfig) { config.MaxDeliver = 3 }},
		{"max ack pending", func(config *jetstream.ConsumerConfig) { config.MaxAckPending = 2 }},
		{"replay policy", func(config *jetstream.ConsumerConfig) {
			config.ReplayPolicy = jetstream.ReplayOriginalPolicy
		}},
		{"filter subject", func(config *jetstream.ConsumerConfig) {
			config.FilterSubject = natswire.DeviceFactWildcard() + ".extra"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := base
			test.mutate(&config)
			if err := validateDeviceFactConsumerConfig(config); err == nil {
				t.Fatalf("%s drift was accepted", test.name)
			}
		})
	}
}

// TestValidateDeviceFactConsumerConfigRejectsDeliveryOverrides pins every
// delivery-affecting override the required pull consumer forbids without a
// broker, so a HeadersOnly or otherwise delivery-altering live consumer is
// rejected even when all required fields still match. It fails if any override
// is accepted or if the rejection does not name the offending field.
func TestValidateDeviceFactConsumerConfigRejectsDeliveryOverrides(t *testing.T) {
	t.Parallel()
	base := deviceFactConsumerConfig()
	if override := deviceFactConsumerDeliveryOverride(base); override != "" {
		t.Fatalf("the required configuration carries the %q override", override)
	}
	tests := []struct {
		override string
		mutate   func(*jetstream.ConsumerConfig)
	}{
		{"headers_only", func(config *jetstream.ConsumerConfig) { config.HeadersOnly = true }},
		{"deliver_subject", func(config *jetstream.ConsumerConfig) {
			config.DeliverSubject = "probe.deliver"
		}},
		{"deliver_group", func(config *jetstream.ConsumerConfig) {
			config.DeliverGroup = "probe-group"
		}},
		{"filter_subjects", func(config *jetstream.ConsumerConfig) {
			config.FilterSubject = ""
			config.FilterSubjects = []string{natswire.DeviceFactWildcard()}
		}},
		{"backoff", func(config *jetstream.ConsumerConfig) {
			config.BackOff = []time.Duration{time.Second}
		}},
		{"rate_limit_bps", func(config *jetstream.ConsumerConfig) { config.RateLimit = 100 }},
		{"sample_freq", func(config *jetstream.ConsumerConfig) { config.SampleFrequency = "10%" }},
		{"max_batch", func(config *jetstream.ConsumerConfig) { config.MaxRequestBatch = 10 }},
		{"max_expires", func(config *jetstream.ConsumerConfig) {
			config.MaxRequestExpires = time.Second
		}},
		{"max_bytes", func(config *jetstream.ConsumerConfig) { config.MaxRequestMaxBytes = 100 }},
		{"inactive_threshold", func(config *jetstream.ConsumerConfig) {
			config.InactiveThreshold = time.Minute
		}},
		{"mem_storage", func(config *jetstream.ConsumerConfig) { config.MemoryStorage = true }},
		{"flow_control", func(config *jetstream.ConsumerConfig) { config.FlowControl = true }},
		{"idle_heartbeat", func(config *jetstream.ConsumerConfig) {
			config.IdleHeartbeat = 5 * time.Second
		}},
		{"opt_start_seq", func(config *jetstream.ConsumerConfig) { config.OptStartSeq = 5 }},
		{"opt_start_time", func(config *jetstream.ConsumerConfig) {
			start := time.Now().Add(-time.Minute)
			config.OptStartTime = &start
		}},
		{"pause_until", func(config *jetstream.ConsumerConfig) {
			pauseUntil := time.Now().Add(time.Minute)
			config.PauseUntil = &pauseUntil
		}},
		{"priority_policy", func(config *jetstream.ConsumerConfig) {
			config.PriorityPolicy = jetstream.PriorityPolicyPinned
		}},
		{"priority_timeout", func(config *jetstream.ConsumerConfig) { config.PinnedTTL = time.Minute }},
		{"priority_groups", func(config *jetstream.ConsumerConfig) {
			config.PriorityGroups = []string{"probe"}
		}},
	}
	for _, test := range tests {
		t.Run(test.override, func(t *testing.T) {
			t.Parallel()
			config := base
			test.mutate(&config)
			err := validateDeviceFactConsumerConfig(config)
			if err == nil {
				t.Fatalf("%s override was accepted", test.override)
			}
			if !strings.Contains(err.Error(), test.override) {
				t.Fatalf("rejection %q does not name %s", err, test.override)
			}
		})
	}
}
