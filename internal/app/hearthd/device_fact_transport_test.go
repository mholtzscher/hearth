package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"log/slog"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
)

// startDeviceFactTransport builds the dedicated Device Fact publication
// connection, the combined epoch fence and the dispatcher against one test NATS
// server, mirroring Core's assembly order. Both the connection and the
// dispatcher are cleaned up with the test.
func startDeviceFactTransport(
	t *testing.T,
	ingest *natsgo.Conn,
	url string,
	logger *slog.Logger,
) (*natsgo.Conn, *devicesnats.DeviceFactDispatcher) {
	t.Helper()
	factConnection, err := natsgo.Connect(url, devicesnats.DeviceFactConnectionOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(factConnection.Close)
	epochs, err := devicesnats.NewDeviceFactEpochs(ingest, factConnection, nil)
	if err != nil {
		t.Fatal(err)
	}
	epochs.Track()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := devicesnats.StartDeviceFactDispatcher(factConnection, validator, epochs, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelDrain()
		_ = dispatcher.Drain(drainContext)
	})
	return factConnection, dispatcher
}
