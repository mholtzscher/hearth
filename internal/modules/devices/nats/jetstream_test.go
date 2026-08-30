package nats //nolint:testpackage // Tests exercise package-private JetStream configuration.

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

func TestProvisionObservationResourcesCreatesAndValidatesRuntimeConfiguration(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, provisionErr := ProvisionObservationResources(context.Background(), js)
	if provisionErr != nil {
		t.Fatal(provisionErr)
	}
	if config := consumer.CachedInfo().Config; config.Name != ObservationConsumerName ||
		config.FilterSubject != natswire.ObservationWildcard() {
		t.Fatalf("consumer = %#v", config)
	}
	stream, streamErr := js.Stream(context.Background(), ObservationStreamName)
	if streamErr != nil {
		t.Fatal(streamErr)
	}
	if subjects := stream.CachedInfo().Config.Subjects; len(subjects) != 1 ||
		subjects[0] != natswire.ObservationWildcard() {
		t.Fatalf("stream subjects = %v", subjects)
	}
	if _, provisionAgainErr := ProvisionObservationResources(context.Background(), js); provisionAgainErr != nil {
		t.Fatalf("second provisioning: %v", provisionAgainErr)
	}
	if validationErr := ValidateObservationResources(context.Background(), js); validationErr != nil {
		t.Fatal(validationErr)
	}
}

func TestProvisionObservationResourcesRejectsMismatchedExistingConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*jetstream.StreamConfig)
	}{
		{"pre-runtime subject", func(config *jetstream.StreamConfig) {
			config.Subjects = []string{"hearth.v1.adapter.*.observation.>"}
		}},
		{"max bytes", func(config *jetstream.StreamConfig) { config.MaxBytes = 42 }},
		{"max messages", func(config *jetstream.StreamConfig) { config.MaxMsgs = 1 }},
		{"max messages per subject", func(config *jetstream.StreamConfig) { config.MaxMsgsPerSubject = 1 }},
		{"max message size", func(config *jetstream.StreamConfig) { config.MaxMsgSize = 1024 }},
		{"no acknowledgements", func(config *jetstream.StreamConfig) { config.NoAck = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, js := startJetStream(t)
			config := observationStreamConfig()
			test.mutate(&config)
			if _, err := js.CreateStream(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			_, err := ProvisionObservationResources(context.Background(), js)
			if err == nil || !strings.Contains(err.Error(), "does not match") {
				t.Fatalf("provisioning error = %v", err)
			}
		})
	}
}
