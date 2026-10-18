package nats //nolint:testpackage // Verifies live module-owned durable compatibility.

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func createAdapterHealthTestStream(t *testing.T, js jetstream.JetStream) jetstream.Stream {
	t.Helper()
	stream, err := js.CreateStream(
		context.Background(),
		jetstream.StreamConfig{
			Name:      "HEARTH_ADAPTER_HEALTH_V1",
			Subjects:  []string{"hearth.v1.core.adapter-health.*"},
			Retention: jetstream.WorkQueuePolicy,
			Storage:   jetstream.FileStorage,
			MaxAge:    0,
			Discard:   jetstream.DiscardNew,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func TestAdapterHealthDurableProvisioning(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	createAdapterHealthTestStream(t, js)
	consumer, err := ProvisionAdapterHealthConsumer(context.Background(), js, "HEARTH_ADAPTER_HEALTH_V1")
	if err != nil {
		t.Fatal(err)
	}
	c := consumer.CachedInfo().Config
	if c.Durable != "hearthd-device-availability-v1" || c.FilterSubject != "hearth.v1.core.adapter-health.*" ||
		c.MaxAckPending != 1 ||
		c.AckWait != 30*time.Second ||
		c.MaxDeliver != -1 ||
		c.AckPolicy != jetstream.AckExplicitPolicy ||
		c.DeliverPolicy != jetstream.DeliverAllPolicy ||
		c.ReplayPolicy != jetstream.ReplayInstantPolicy {
		t.Fatalf("durable: %+v", c)
	}
	if _, provisionErr := ProvisionAdapterHealthConsumer(
		context.Background(),
		js,
		"HEARTH_ADAPTER_HEALTH_V1",
	); provisionErr != nil {
		t.Fatal(provisionErr)
	}
}

func TestAdapterHealthDurableRejectsIncompatibleConfiguration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*jetstream.ConsumerConfig)
	}{
		{"multiple pending", func(c *jetstream.ConsumerConfig) { c.MaxAckPending = 2 }},
		{"finite attempts", func(c *jetstream.ConsumerConfig) { c.MaxDeliver = 2 }},
		{"wrong ack wait", func(c *jetstream.ConsumerConfig) { c.AckWait = time.Second }},
		{"partial filter", func(c *jetstream.ConsumerConfig) { c.FilterSubject = "hearth.v1.core.adapter-health.one" }},
		{"headers only", func(c *jetstream.ConsumerConfig) { c.HeadersOnly = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, js := startJetStream(t)
			stream := createAdapterHealthTestStream(t, js)
			config := adapterHealthConsumerConfig()
			tc.change(&config)
			if _, err := stream.CreateConsumer(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			if _, err := ProvisionAdapterHealthConsumer(
				context.Background(),
				js,
				"HEARTH_ADAPTER_HEALTH_V1",
			); err == nil {
				t.Fatal("accepted incompatible durable")
			}
		})
	}
}
