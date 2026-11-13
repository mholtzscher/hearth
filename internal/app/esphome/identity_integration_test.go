//go:build integration

package esphome //nolint:testpackage // Exercises the process against real Core registration and State persistence.

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestRunRegistersIdenticalDevicesWithIndependentState(t *testing.T) {
	t.Parallel()
	natsURL, service := coreFixture(t)
	first, firstConnected := serveCO2Device(t, "02:00:00:00:00:01", "Office Air", 650)
	second, secondConnected := serveCO2Device(t, "02:00:00:00:00:02", "Bedroom Air", 1200)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	runContext, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		finished <- Run(runContext, Config{
			AdapterID: "esphome", NATSURL: natsURL, Devices: []DeviceConfig{first, second},
		}, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		stop()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	})
	for _, connected := range []<-chan *nativeapitest.Session{firstConnected, secondConnected} {
		select {
		case peer := <-connected:
			t.Cleanup(peer.Close)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	entities := waitForIdenticalDeviceState(ctx, t, service)
	if entities[0].Entity.ID == entities[1].Entity.ID || entities[0].Entity.DeviceID == entities[1].Entity.DeviceID {
		t.Fatal("identical physical Devices share canonical IDs")
	}
	want := map[string]string{"Office Air": "650", "Bedroom Air": "1200"}
	for _, entity := range entities {
		device, err := service.GetDevice(ctx, devices.GetDeviceParams{ID: entity.Entity.DeviceID, EntityLimit: 10})
		if err != nil {
			t.Fatal(err)
		}
		value, known := want[device.Device.Name]
		if !known || entity.Entity.Name != "CO2" || string(entity.State.Value) != value {
			t.Fatalf("%s CO2 = %s, want %s", device.Device.Name, entity.State.Value, value)
		}
		delete(want, device.Device.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing Devices: %v", want)
	}
}

func serveCO2Device(t *testing.T, mac, name string, value float32) (DeviceConfig, <-chan *nativeapitest.Session) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connected := make(chan *nativeapitest.Session, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		device := nativeapitest.Device{
			Plaintext: true,
			Info:      &apipb.DeviceInfoResponse{MacAddress: mac, Name: name},
			Entities: []proto.Message{&apipb.ListEntitiesSensorResponse{
				Key: 1, Name: "CO2", DeviceClass: "carbon_dioxide", UnitOfMeasurement: "ppm",
			}},
			States: []proto.Message{&apipb.SensorStateResponse{Key: 1, State: value}},
		}
		connected <- device.Serve(connection)
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return DeviceConfig{MAC: mac, Address: host, Port: &port, InsecurePlaintext: true}, connected
}

func waitForIdenticalDeviceState(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
) []devices.EntityWithState {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 2 && page.Items[0].State != nil && page.Items[1].State != nil &&
			page.Items[0].Availability.Status == devices.EntityAvailabilityAvailable &&
			page.Items[1].Availability.Status == devices.EntityAvailabilityAvailable {
			return page.Items
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"both identical Devices must register and publish State: got %d Entities: %v",
				len(page.Items),
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}
