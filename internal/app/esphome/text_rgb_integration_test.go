//go:build integration

package esphome //nolint:testpackage // Crosses the production adapter, Native API, SDK, and real Core.

import (
	"context"
	"encoding/json"
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

//nolint:gocognit,tparallel // Serial runtimes share persisted ownership; commands target only scripted peers.
func TestRunTextRGBAndEffectsAcrossTwoDevicesAndRestart(t *testing.T) {
	t.Parallel()
	natsURL, service := coreFixture(t)
	first, firstConnected := serveFeatureDevice(t, "02:00:00:00:00:01", "Office", "Improved", 10, false)
	second, secondConnected := serveFeatureDevice(t, "02:00:00:00:00:02", "Bedroom", "Good", 20, true)
	priorIDs := map[string]devices.EntityID{}
	for _, phase := range []string{"first runtime", "restart"} {
		if !t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			runContext, stop := context.WithCancel(ctx)
			defer stop()
			since := time.Now().UTC()
			finished := make(chan error, 1)
			go func() {
				finished <- Run(runContext, Config{AdapterID: "esphome", NATSURL: natsURL,
					Devices: []DeviceConfig{first, second}}, slog.New(slog.DiscardHandler))
			}()
			t.Cleanup(func() {
				stop()
				if err := <-finished; err != nil {
					t.Error(err)
				}
			})
			peers := make([]*nativeapitest.Session, 0, 2)
			for _, connected := range []<-chan *nativeapitest.Session{firstConnected, secondConnected} {
				select {
				case peer := <-connected:
					peers = append(peers, peer)
					t.Cleanup(peer.Close)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			entities := waitFeatureEntities(ctx, t, service, since)
			byName := map[string]devices.EntityWithState{}
			for _, entity := range entities {
				device, err := service.GetDevice(
					ctx,
					devices.GetDeviceParams{ID: entity.Entity.DeviceID, EntityLimit: 20},
				)
				if err != nil {
					t.Fatal(err)
				}
				key := device.Device.Name + "/" + entity.Entity.Name
				byName[key] = entity
				if phase == "restart" && priorIDs[key] != entity.Entity.ID {
					t.Fatalf("restart changed %s identity", key)
				}
				priorIDs[key] = entity.Entity.ID
			}
			for key, want := range map[string]string{"Office/VOC Quality": `"Improved"`, "Bedroom/VOC Quality": `"Good"`,
				"Office/Carbon Monoxide": "10", "Bedroom/Carbon Monoxide": "20"} {
				if got := string(byName[key].State.Value); got != want {
					t.Fatalf("%s = %s, want %s", key, got, want)
				}
			}
			voc := byName["Office/VOC Quality"]
			var support struct {
				Operations map[string]json.RawMessage `json:"operations"`
			}
			if err := json.Unmarshal(voc.Entity.Support, &support); err != nil {
				t.Fatal(err)
			}
			if voc.Entity.TypeID != "hearth.textsensor/v1" || len(support.Operations) != 0 {
				t.Fatalf("VOC is not read-only text: %+v", voc.Entity)
			}
			if _, err := service.ExecuteCommand(ctx, devices.CommandInput{EntityID: voc.Entity.ID,
				OperationName: devices.OperationNameSet, Parameters: []byte(`{"value":"fake"}`)}); err == nil {
				t.Fatal("VOC write accepted")
			}
			if phase == "first runtime" {
				if _, err := service.ExecuteCommand(
					ctx,
					devices.CommandInput{
						EntityID:      byName["Office/RGB Light Effect"].Entity.ID,
						OperationName: devices.OperationNameSet,
						Parameters:    []byte(`{"value":"Fast Pulse"}`),
					},
				); err == nil {
					t.Fatal("effect change while off was accepted")
				}
				select {
				case message := <-peers[0].Received():
					t.Fatalf("off effect sent a frame: %v", message)
				default:
				}
				for _, parameters := range []string{`{"red":255,"green":255,"blue":255}`,
					`{"red":128,"green":128,"blue":128}`, `{"red":0,"green":0,"blue":0}`, `{"red":1,"green":254,"blue":17}`} {
					runFeatureCommand(ctx, t, service, peers[0], byName["Office/RGB Light Color"].Entity.ID,
						[]byte(parameters), true)
				}
				runFeatureCommand(ctx, t, service, peers[1], byName["Bedroom/RGB Light Effect"].Entity.ID,
					[]byte(`{"value":"Fast Pulse"}`), false)
			}
			for _, peer := range peers {
				select {
				case message := <-peer.Received():
					t.Fatalf("unexpected write: %v", message)
				default:
				}
			}
		}) {
			break
		}
	}
}

func serveFeatureDevice(
	t *testing.T,
	mac, name, text string,
	gas float32,
	on bool,
) (DeviceConfig, <-chan *nativeapitest.Session) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connected := make(chan *nativeapitest.Session, 2)
	go func() {
		for range 2 {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			entities := []proto.Message{
				&apipb.ListEntitiesTextSensorResponse{Key: 1, Name: "VOC Quality"},
				&apipb.ListEntitiesLightResponse{
					Key:                 2,
					Name:                "RGB Light",
					SupportedColorModes: []apipb.ColorMode{apipb.ColorMode_COLOR_MODE_RGB},
					Effects:             []string{"None", "Slow Pulse", "Fast Pulse"},
				},
			}
			states := []proto.Message{
				&apipb.TextSensorStateResponse{Key: 1, State: text},
				&apipb.LightStateResponse{
					Key:             2,
					State:           on,
					Brightness:      0.3,
					ColorBrightness: 1,
					ColorMode:       apipb.ColorMode_COLOR_MODE_RGB,
					Red:             1,
					Effect:          "None",
				},
			}
			for index, sensor := range []string{"Carbon Monoxide", "Methane", "Ethanol", "Hydrogen", "Ammonia", "Nitrogen Dioxide"} {
				key := uint32(index + 3)
				entities = append(
					entities,
					&apipb.ListEntitiesSensorResponse{Key: key, Name: sensor, UnitOfMeasurement: "ppm"},
				)
				states = append(states, &apipb.SensorStateResponse{Key: key, State: gas + float32(index)})
			}
			device := nativeapitest.Device{
				Plaintext: true,
				Info:      &apipb.DeviceInfoResponse{MacAddress: mac, Name: name},
				Entities:  entities,
				States:    states,
			}
			connected <- device.Serve(connection)
		}
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

func waitFeatureEntities(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	since time.Time,
) []devices.EntityWithState {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		ready := len(page.Items) == 22
		for _, entity := range page.Items {
			if entity.State == nil || !entity.State.ObservedAt.After(since) ||
				entity.Availability.Status != devices.EntityAvailabilityAvailable {
				ready = false
			}
		}
		if ready {
			return page.Items
		}
		select {
		case <-ctx.Done():
			t.Fatalf("feature entities not ready: %d Entities: %v", len(page.Items), ctx.Err())
		case <-ticker.C:
		}
	}
}

func runFeatureCommand(ctx context.Context, t *testing.T, service *devices.Service, peer *nativeapitest.Session,
	entityID devices.EntityID, parameters []byte, color bool) {
	t.Helper()
	type outcome struct {
		result devices.CommandResult
		err    error
	}
	done := make(chan outcome, 1)
	state := &apipb.LightStateResponse{Key: 2, State: !color, Brightness: 0.3, ColorBrightness: 1,
		ColorMode: apipb.ColorMode_COLOR_MODE_RGB, Red: 1, Effect: "None"}
	go func() {
		result, err := service.ExecuteCommand(ctx, devices.CommandInput{EntityID: entityID,
			OperationName: devices.OperationNameSet, Parameters: parameters})
		done <- outcome{result, err}
	}()
	select {
	case message := <-peer.Received():
		request, ok := message.Message.(*apipb.LightCommandRequest)
		if !ok || request.GetKey() != 2 || request.GetHasState() || request.GetHasBrightness() {
			t.Fatalf("unexpected light write: %v", message)
		}
		if color {
			applyFeatureRGB(t, request, parameters, state)
		} else if !request.GetHasEffect() || request.GetEffect() != "Fast Pulse" || request.GetHasRgb() || request.GetHasColorMode() || request.GetHasColorBrightness() {
			t.Fatalf("effect write = %v", request)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !color {
		state.Effect = "Fast Pulse"
	}
	if err := peer.Push(state); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.result.Outcome != devices.OutcomeObserved || result.result.Value == nil {
			t.Fatalf("command outcome = %+v, %v", result.result, result.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func applyFeatureRGB(
	t *testing.T,
	request *apipb.LightCommandRequest,
	parameters []byte,
	state *apipb.LightStateResponse,
) {
	t.Helper()
	var expected struct {
		Red   float32 `json:"red"`
		Green float32 `json:"green"`
		Blue  float32 `json:"blue"`
	}
	if err := json.Unmarshal(parameters, &expected); err != nil {
		t.Fatal(err)
	}
	if !request.GetHasRgb() || !request.GetHasColorMode() || !request.GetHasColorBrightness() ||
		request.GetRed() != expected.Red/255 || request.GetGreen() != expected.Green/255 || request.GetBlue() != expected.Blue/255 ||
		request.GetColorBrightness() != max(expected.Red, expected.Green, expected.Blue)/255 || request.GetHasEffect() {
		t.Fatalf("RGB write = %v", request)
	}
	// ESPHome normalizes RGB separately from color brightness, including black -> normalized white at zero amplitude.
	maximum := max(request.GetRed(), request.GetGreen(), request.GetBlue())
	state.Red, state.Green, state.Blue = 1, 1, 1
	if maximum != 0 {
		state.Red = request.GetRed() / maximum
		state.Green = request.GetGreen() / maximum
		state.Blue = request.GetBlue() / maximum
	}
	state.ColorBrightness = request.GetColorBrightness()
}
