package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
)

// startDeviceFactRelay starts the production Device Fact relay over one shared
// JetStream context and a durable outbox, mirroring Core's start order: stream
// first, relay before any fact-producing consumer. The relay is drained when
// the test ends so no publication goroutine outlives it.
func startDeviceFactRelay(
	t *testing.T,
	js jetstream.JetStream,
	outbox devices.DeviceFactOutbox,
) *devicesnats.DeviceFactRelay {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	relay, err := devicesnats.StartDeviceFactRelay(
		js, outbox, validator, slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelDrain()
		_ = relay.Drain(drainContext)
	})
	return relay
}
