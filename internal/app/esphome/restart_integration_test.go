//go:build integration

package esphome //nolint:testpackage // Exercises discovery restart against persisted real-Core ownership.

import (
	"context"
	"log/slog"
	"testing"
	"time"

	esphomeadapter "github.com/mholtzscher/hearth/internal/adapters/esphome"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type restartDiscovery struct {
	records chan esphomeadapter.DiscoveryRecord
	started chan struct{}
}

func (source restartDiscovery) Run(ctx context.Context, records chan<- esphomeadapter.DiscoveryRecord) error {
	close(source.started)
	for {
		select {
		case <-ctx.Done():
			return nil
		case record := <-source.records:
			select {
			case records <- record:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

//nolint:gocognit,tparallel // One recovery journey owns both runtimes; serial phases share persisted mappings.
func TestRunRestartsWithOwnedDiscoveredEntities(t *testing.T) {
	t.Parallel()
	natsURL, service := coreFixture(t)
	logger := slog.New(slog.DiscardHandler)
	for _, phase := range []struct {
		name      string
		discovery bool
		firstCO2  float32
		secondCO2 float32
	}{
		{name: "enroll", firstCO2: 650, secondCO2: 1200},
		{name: "restart", discovery: true, firstCO2: 700, secondCO2: 1300},
	} {
		if !t.Run(phase.name, func(t *testing.T) {
			first, firstConnected := serveCO2Device(t, "02:00:00:00:00:01", "Office Air", phase.firstCO2)
			second, secondConnected := serveCO2Device(t, "02:00:00:00:00:02", "Bedroom Air", phase.secondCO2)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			runContext, stop := context.WithCancel(ctx)
			defer stop()
			source := restartDiscovery{records: make(chan esphomeadapter.DiscoveryRecord), started: make(chan struct{})}
			finished := make(chan struct{})
			var runErr error
			go func() {
				defer close(finished)
				config := Config{AdapterID: "esphome", NATSURL: natsURL}
				if phase.discovery {
					config.Discovery = DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true}
					runErr = runWithDiscovery(runContext, config, logger, source)
				} else {
					config.Devices = []DeviceConfig{first, second}
					runErr = Run(runContext, config, logger)
				}
			}()
			t.Cleanup(func() {
				stop()
				<-finished
				if runErr != nil {
					t.Error(runErr)
				}
			})
			if phase.discovery {
				select {
				case <-source.started:
				case <-finished:
					t.Fatalf("restart stopped before discovery: %v", runErr)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				waitForRestartState(ctx, t, service, true)
				for _, device := range []DeviceConfig{first, second} {
					mac, err := esphomeadapter.ParseMAC(device.MAC)
					if err != nil {
						t.Fatal(err)
					}
					select {
					case source.records <- esphomeadapter.DiscoveryRecord{
						MAC: mac, Address: device.Address, Port: uint16(*device.Port),
					}:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
			}
			for _, connected := range []<-chan *nativeapitest.Session{firstConnected, secondConnected} {
				select {
				case peer := <-connected:
					t.Cleanup(peer.Close)
				case <-finished:
					t.Fatalf("runtime stopped: %v", runErr)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if phase.discovery {
				waitForRestartState(ctx, t, service, false)
			} else {
				_ = waitForIdenticalDeviceState(ctx, t, service)
			}
		}) {
			break
		}
	}
}

func waitForRestartState(ctx context.Context, t *testing.T, service *devices.Service, pending bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		matched := map[string]struct{}{}
		for _, entity := range page.Items {
			if restartStateMatches(entity, pending) {
				key := string(entity.Entity.ID)
				if !pending {
					key = string(entity.State.Value)
				}
				matched[key] = struct{}{}
			}
		}
		if len(page.Items) == 2 && len(matched) == 2 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"restart State pending=%t: matched %d of %d Entities: %v",
				pending,
				len(matched),
				len(page.Items),
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func restartStateMatches(entity devices.EntityWithState, pending bool) bool {
	availability := entity.Availability
	if pending {
		return availability.Status == devices.EntityAvailabilityUnavailable && availability.Reason != nil &&
			availability.Reason.Code == "adapter.hearth-adapter-esphome.discovery_pending"
	}
	return availability.Status == devices.EntityAvailabilityAvailable && entity.State != nil &&
		(string(entity.State.Value) == "700" || string(entity.State.Value) == "1300")
}
