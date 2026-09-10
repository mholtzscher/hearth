package nats //nolint:testpackage // Tests exercise package-private JetStream configuration.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// This test protects the dedicated Device Event stream and consumer settings
// and fails if Device Events share Observation resources or lose their
// independent limits, delivery policy, or acknowledgement policy.
func TestProvisionDeviceEventResourcesCreatesAndValidatesRuntimeConfiguration(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, provisionErr := ProvisionDeviceEventResources(context.Background(), js)
	if provisionErr != nil {
		t.Fatal(provisionErr)
	}
	consumerConfig := consumer.CachedInfo().Config
	if consumerConfig.Name != DeviceEventConsumerName ||
		consumerConfig.FilterSubject != natswire.DeviceEventWildcard() ||
		consumerConfig.DeliverPolicy != jetstream.DeliverAllPolicy ||
		consumerConfig.AckPolicy != jetstream.AckExplicitPolicy ||
		consumerConfig.AckWait != 30*time.Second ||
		consumerConfig.MaxDeliver != -1 ||
		consumerConfig.MaxAckPending != 1 ||
		consumerConfig.ReplayPolicy != jetstream.ReplayInstantPolicy {
		t.Fatalf("consumer = %#v", consumerConfig)
	}
	stream, streamErr := js.Stream(context.Background(), DeviceEventStreamName)
	if streamErr != nil {
		t.Fatal(streamErr)
	}
	streamConfig := stream.CachedInfo().Config
	if len(streamConfig.Subjects) != 1 || streamConfig.Subjects[0] != natswire.DeviceEventWildcard() ||
		streamConfig.Subjects[0] == natswire.ObservationWildcard() ||
		streamConfig.Storage != jetstream.FileStorage ||
		streamConfig.Retention != jetstream.LimitsPolicy ||
		streamConfig.MaxAge != 7*24*time.Hour ||
		streamConfig.MaxBytes != 1<<30 ||
		streamConfig.MaxMsgSize != 4*1024 ||
		streamConfig.MaxMsgs != -1 ||
		streamConfig.MaxMsgsPerSubject != -1 ||
		streamConfig.Discard != jetstream.DiscardOld ||
		streamConfig.NoAck {
		t.Fatalf("stream = %#v", streamConfig)
	}
	if _, provisionAgainErr := ProvisionDeviceEventResources(context.Background(), js); provisionAgainErr != nil {
		t.Fatalf("second provisioning: %v", provisionAgainErr)
	}
	if validationErr := ValidateDeviceEventResources(context.Background(), js); validationErr != nil {
		t.Fatal(validationErr)
	}
}

func TestProvisionDeviceEventResourcesRejectsMismatchedExistingConfiguration(t *testing.T) {
	t.Parallel()
	streamTests := []struct {
		name   string
		mutate func(*jetstream.StreamConfig)
	}{
		{"observation subject", func(config *jetstream.StreamConfig) {
			config.Subjects = []string{natswire.ObservationWildcard()}
		}},
		{"unscoped subject", func(config *jetstream.StreamConfig) {
			config.Subjects = []string{"hearth.v1.adapter.*.runtime.*.device-event.>"}
		}},
		{"max age", func(config *jetstream.StreamConfig) { config.MaxAge = time.Hour }},
		{"max bytes", func(config *jetstream.StreamConfig) { config.MaxBytes = 42 }},
		{"max message size", func(config *jetstream.StreamConfig) { config.MaxMsgSize = 1024 }},
		{"max messages", func(config *jetstream.StreamConfig) { config.MaxMsgs = 1 }},
		{"max messages per subject", func(config *jetstream.StreamConfig) { config.MaxMsgsPerSubject = 1 }},
		{"discard policy", func(config *jetstream.StreamConfig) { config.Discard = jetstream.DiscardNew }},
		{"no acknowledgements", func(config *jetstream.StreamConfig) { config.NoAck = true }},
	}
	for _, test := range streamTests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, js := startJetStream(t)
			config := deviceEventStreamConfig()
			test.mutate(&config)
			if _, err := js.CreateStream(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			_, err := ProvisionDeviceEventResources(context.Background(), js)
			if err == nil || !strings.Contains(err.Error(), "does not match") {
				t.Fatalf("provisioning error = %v", err)
			}
		})
	}

	consumerTests := []struct {
		name   string
		mutate func(*jetstream.ConsumerConfig)
	}{
		{"ack wait", func(config *jetstream.ConsumerConfig) { config.AckWait = time.Minute }},
		{"pending limit", func(config *jetstream.ConsumerConfig) { config.MaxAckPending = 16 }},
		{"delivery policy", func(config *jetstream.ConsumerConfig) {
			config.DeliverPolicy = jetstream.DeliverNewPolicy
		}},
		{"ack policy", func(config *jetstream.ConsumerConfig) { config.AckPolicy = jetstream.AckAllPolicy }},
		{"replay policy", func(config *jetstream.ConsumerConfig) {
			config.ReplayPolicy = jetstream.ReplayOriginalPolicy
		}},
		{"redelivery limit", func(config *jetstream.ConsumerConfig) { config.MaxDeliver = 5 }},
	}
	for _, test := range consumerTests {
		t.Run("consumer "+test.name, func(t *testing.T) {
			t.Parallel()
			_, _, js := startJetStream(t)
			stream, err := js.CreateStream(context.Background(), deviceEventStreamConfig())
			if err != nil {
				t.Fatal(err)
			}
			config := deviceEventConsumerConfig()
			test.mutate(&config)
			if _, createErr := stream.CreateConsumer(context.Background(), config); createErr != nil {
				t.Fatal(createErr)
			}
			_, err = ProvisionDeviceEventResources(context.Background(), js)
			if err == nil || !strings.Contains(err.Error(), "does not match") {
				t.Fatalf("provisioning error = %v", err)
			}
		})
	}
}
