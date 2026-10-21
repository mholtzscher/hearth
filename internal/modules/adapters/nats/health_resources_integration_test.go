//go:build integration

package nats //nolint:testpackage // Exercises live configuration compatibility with package-owned defaults.

import (
	"context"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func startHealthJetStream(t *testing.T) (jetstream.JetStream, *natsgo.Conn) {
	t.Helper()
	server, err := natsserver.NewServer(
		&natsserver.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(func() { server.Shutdown(); server.WaitForShutdown() })
	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS did not start")
	}
	conn, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	return js, conn
}

func TestHealthStreamProvisioningAndCapacity(t *testing.T) {
	t.Parallel()
	js, _ := startHealthJetStream(t)
	ctx := context.Background()
	if err := ProvisionHealthStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionHealthStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, HealthStreamName)
	if err != nil {
		t.Fatal(err)
	}
	c := stream.CachedInfo().Config
	if c.Retention != jetstream.WorkQueuePolicy || c.Discard != jetstream.DiscardNew || c.MaxBytes != 1<<30 ||
		c.MaxAge != 0 ||
		c.MaxMsgSize != 16384 ||
		c.Duplicates != 2*time.Minute {
		t.Fatalf("configuration: %+v", c)
	}
	// Exercise the same retention/discard policy with a small broker capacity;
	// no test-only production size knob and no GiB allocation is needed.
	c.MaxMsgs = 1
	if _, updateErr := js.UpdateStream(ctx, c); updateErr != nil {
		t.Fatal(updateErr)
	}
	if validationErr := ValidateHealthStream(ctx, js); validationErr == nil {
		t.Fatal("accepted altered capacity")
	}
	if _, publishErr := js.Publish(ctx, "hearth.v1.core.adapter-health.one", []byte("first")); publishErr != nil {
		t.Fatal(publishErr)
	}
	if _, publishErr := js.Publish(ctx, "hearth.v1.core.adapter-health.two", []byte("second")); publishErr == nil {
		t.Fatal("full work queue accepted another message")
	}
	retained, err := stream.GetMsg(ctx, 1)
	if err != nil || string(retained.Data) != "first" {
		t.Fatalf("unconsumed work lost: %v %v", retained, err)
	}
	if deleteErr := stream.DeleteMsg(ctx, 1); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if _, publishErr := js.Publish(ctx, "hearth.v1.core.adapter-health.two", []byte("second")); publishErr != nil {
		t.Fatal(publishErr)
	}
}

func TestHealthStreamRejectsIncompatibleExistingResources(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		change func(*jetstream.StreamConfig)
	}{
		{"age expiry", func(c *jetstream.StreamConfig) { c.MaxAge = time.Hour }},
		{"eviction", func(c *jetstream.StreamConfig) { c.Discard = jetstream.DiscardOld }},
		{"wrong retention", func(c *jetstream.StreamConfig) { c.Retention = jetstream.LimitsPolicy }},
		{"transformed subject", func(c *jetstream.StreamConfig) {
			c.SubjectTransform = &jetstream.SubjectTransformConfig{
				Source:      "hearth.v1.core.adapter-health.*",
				Destination: "hidden.$1",
			}
		}},
		{"wrong message bound", func(c *jetstream.StreamConfig) { c.MaxMsgSize = 1024 }},
		{"wrong duplicate window", func(c *jetstream.StreamConfig) { c.Duplicates = time.Minute }},
		{"no publisher acknowledgement", func(c *jetstream.StreamConfig) { c.NoAck = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js, _ := startHealthJetStream(t)
			config := healthStreamConfig()
			tc.change(&config)
			if _, err := js.CreateStream(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			if err := ProvisionHealthStream(context.Background(), js); err == nil {
				t.Fatal("accepted incompatible existing resource")
			}
		})
	}
}
