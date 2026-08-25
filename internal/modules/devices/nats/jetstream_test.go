package nats

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestProvisionObservationResourcesCreatesAndValidatesRequiredConfiguration(t *testing.T) {
	_, _, js := startJetStream(t)
	consumer, err := ProvisionObservationResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.CachedInfo().Config.Name != ObservationConsumerName {
		t.Fatalf("consumer = %#v", consumer.CachedInfo().Config)
	}
	if _, err := ProvisionObservationResources(context.Background(), js); err != nil {
		t.Fatalf("second provisioning: %v", err)
	}
	if err := ValidateObservationResources(context.Background(), js); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionObservationResourcesRejectsMismatchedExistingConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*jetstream.StreamConfig)
	}{
		{"max bytes", func(config *jetstream.StreamConfig) { config.MaxBytes = 42 }},
		{"max messages", func(config *jetstream.StreamConfig) { config.MaxMsgs = 1 }},
		{"max messages per subject", func(config *jetstream.StreamConfig) { config.MaxMsgsPerSubject = 1 }},
		{"max message size", func(config *jetstream.StreamConfig) { config.MaxMsgSize = 1024 }},
		{"no acknowledgements", func(config *jetstream.StreamConfig) { config.NoAck = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
