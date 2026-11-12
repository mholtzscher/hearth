//go:build integration

package esphome //nolint:testpackage // Exercises the production process assembly.

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestRunObservesAndSatisfiesCommandsThroughRealCore(t *testing.T) {
	t.Parallel()
	natsURL, service := coreFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	key := nativeapi.PSK{1, 2, 3}
	connected := make(chan *nativeapitest.Session, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		device := nativeapitest.Device{
			PSK:  key,
			MAC:  "020000000001",
			Name: "fixture",
			Info: &apipb.DeviceInfoResponse{MacAddress: "02:00:00:00:00:01", Name: "fixture"},
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
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "key")
	if err = os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(key[:])), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	runContext, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		finished <- Run(runContext, Config{AdapterID: "esphome", NATSURL: natsURL, Devices: []DeviceConfig{{
			MAC: "02:00:00:00:00:01", Address: host, Port: &port, EncryptionKeyFile: keyPath,
		}}}, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		stop()
		if runErr := <-finished; runErr != nil {
			t.Error(runErr)
		}
	})
	var peer *nativeapitest.Session
	select {
	case peer = <-connected:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	t.Cleanup(peer.Close)
	relay := waitForEntities(ctx, t, service)
	// Even an unchanged request must cross the wire, then satisfy from the current live cache.
	result, err := service.ExecuteCommand(
		ctx,
		devices.CommandInput{
			EntityID:      relay,
			OperationName: devices.OperationNameSet,
			Parameters:    []byte(`{"value":false}`),
		},
	)
	if err != nil || result.Outcome != devices.OutcomeObserved || result.Value == nil ||
		string(*result.Value) != "false" {
		t.Fatalf("cached result = %+v, %v", result, err)
	}
	select {
	case <-peer.Received():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	type outcome struct {
		result devices.CommandResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		value, commandErr := service.ExecuteCommand(
			ctx,
			devices.CommandInput{
				EntityID:      relay,
				OperationName: devices.OperationNameSet,
				Parameters:    []byte(`{"value":true}`),
			},
		)
		done <- outcome{value, commandErr}
	}()
	select {
	case message := <-peer.Received():
		request, ok := message.Message.(*apipb.SwitchCommandRequest)
		if !ok || request.GetKey() != 2 || !request.GetState() {
			t.Fatalf("write = %v", message.Message)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = peer.Push(&apipb.SwitchStateResponse{Key: 2, State: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-done:
		if value.err != nil || value.result.Outcome != devices.OutcomeObserved || value.result.Value == nil ||
			string(*value.result.Value) != "true" {
			t.Fatalf("fresh result = %+v, %v", value.result, value.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

//nolint:gocognit // Polling waits for a coherent public projection, not a fixed delay.
func waitForEntities(ctx context.Context, t *testing.T, service *devices.Service) devices.EntityID {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 2 {
			var relay devices.EntityID
			ready := true
			for _, entity := range page.Items {
				if entity.State == nil || entity.Availability.Status != devices.EntityAvailabilityAvailable {
					ready = false
					continue
				}
				switch entity.Entity.Name {
				case "Temperature":
					if string(entity.State.Value) != "-23900" {
						t.Fatalf("temperature = %s", entity.State.Value)
					}
				case "Relay":
					relay = entity.Entity.ID
				}
			}
			if ready && relay != "" {
				return relay
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("entities not ready: ", ctx.Err())
		case <-ticker.C:
		}
	}
}
