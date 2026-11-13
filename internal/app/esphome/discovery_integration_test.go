//go:build integration

package esphome //nolint:testpackage // Verifies discovery configuration and dynamic registration through Core.

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	esphomeadapter "github.com/mholtzscher/hearth/internal/adapters/esphome"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
)

type oneDeviceDiscovery struct {
	record esphomeadapter.DiscoveryRecord
	done   chan struct{}
}

func (source oneDeviceDiscovery) Run(ctx context.Context, records chan<- esphomeadapter.DiscoveryRecord) error {
	defer close(source.done)
	for range 2 {
		select {
		case records <- source.record:
		case <-ctx.Done():
			return nil
		}
	}
	<-ctx.Done()
	return nil
}

func TestRunAutoRegistersDiscoveredPlaintextDeviceThroughRealCore(t *testing.T) {
	t.Parallel()
	natsURL, service := coreFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connected := make(chan *nativeapitest.Session, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		device := nativeapitest.Device{
			Plaintext: true,
			APIMajor:  1,
			APIMinor:  10,
			Info:      &apipb.DeviceInfoResponse{MacAddress: "02:00:00:00:00:01", Name: "discovered"},
			Entities: []proto.Message{
				&apipb.ListEntitiesSensorResponse{
					Key:               1,
					Name:              "Temperature",
					UnitOfMeasurement: "°C",
					DeviceClass:       "temperature",
				},
				&apipb.ListEntitiesSwitchResponse{Key: 2, Name: "Relay"},
			},
			States: []proto.Message{
				&apipb.SensorStateResponse{Key: 1, State: -23.9},
				&apipb.SwitchStateResponse{Key: 2},
			},
		}
		connected <- device.Serve(conn)
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := esphomeadapter.ParseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatal(err)
	}
	source := oneDeviceDiscovery{
		record: esphomeadapter.DiscoveryRecord{MAC: mac, Address: host, Port: uint16(port)},
		done:   make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	runContext, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		finished <- runWithDiscovery(runContext, Config{AdapterID: "esphome", NATSURL: natsURL,
			Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true}}, slog.New(slog.DiscardHandler), source)
	}()
	t.Cleanup(func() {
		stop()
		if runErr := <-finished; runErr != nil {
			t.Error(runErr)
		}
		select {
		case <-source.done:
		default:
			t.Error("source not joined before session close")
		}
	})
	var peer *nativeapitest.Session
	select {
	case peer = <-connected:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	t.Cleanup(peer.Close)
	// This crosses config, policy, worker, legacy plaintext API, SDK/NATS, SQLite, and Core's public read service.
	_ = waitForEntities(ctx, t, service)
	select {
	case command := <-peer.Received():
		t.Fatalf("read-only discovery emitted a Command: %+v", command)
	default:
	}
}
