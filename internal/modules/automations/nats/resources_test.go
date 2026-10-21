package nats //nolint:testpackage // Tests exercise package-private consumer provisioning.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// Provisioning must reject an empty stream name.
func TestProvisionDeviceFactConsumerRequiresStreamName(t *testing.T) {
	t.Parallel()
	if _, err := ProvisionDeviceFactConsumer(context.Background(), nil, ""); err == nil {
		t.Fatal("provisioning accepted an empty stream name")
	}
}

// Check each required field directly, independent of broker normalization.
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

// Delivery overrides must be rejected by field name even when required settings match.
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
