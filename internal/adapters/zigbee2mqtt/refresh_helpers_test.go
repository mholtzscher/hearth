package zigbee2mqtt //nolint:testpackage // Fixtures use the existing MQTT/runtime seams.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type refreshFixture struct {
	z2m         *Adapter
	session     *fakeSession
	connection  *fakeConnection
	snapshot    routeSnapshot
	temperature string
	power       string
	unsupported string
}

func newRefreshFixture(t *testing.T) refreshFixture {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	startCoordinator(t, z2m)
	light := eligibleDevice()
	light.Definition.Exposes = append(light.Definition.Exposes, upstreamExpose{
		Type: "numeric", Name: "temperature", Property: "temperature", Unit: "°C", Access: 5,
	})
	sensor := eligibleDevice()
	sensor.IEEEAddress = "0x00124b0000000002"
	sensor.FriendlyName = "report-only"
	sensor.Definition.Exposes = []upstreamExpose{
		{Type: "numeric", Name: "temperature", Property: "temperature", Unit: "°C", Access: 1},
	}
	payload, err := json.Marshal([]upstreamDevice{light, sensor})
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := discoverInventory(payload)
	if err != nil || len(inventory.Devices) != 2 {
		t.Fatalf("inventory = %#v, %v", inventory, err)
	}
	fixture := refreshFixture{z2m: z2m, session: session, connection: newFakeConnection(recorder)}
	for _, device := range inventory.Devices {
		binding := adapter.Binding{BindingKey: device.Registration.BindingKey, DeviceID: "dev-test"}
		for _, entity := range device.Entities {
			id, mintErr := uuid.NewV7()
			if mintErr != nil {
				t.Fatal(mintErr)
			}
			entityID := "ent_" + id.String()
			// Core-disabled Entities remain eligible for a refresh.
			binding.Entities = append(
				binding.Entities,
				adapter.EntityBinding{Key: entity.Descriptor.Key, EntityID: entityID, Enabled: false},
			)
			switch {
			case device.FriendlyName == "report-only":
				fixture.unsupported = entityID
			case entity.Descriptor.Key == "temperature":
				fixture.temperature = entityID
			case entity.Descriptor.Key == "power":
				fixture.power = entityID
			}
		}
		session.bindings[binding.BindingKey] = binding
	}
	fixture.snapshot, err = z2m.buildRouteSnapshot(context.Background(), 1, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.temperature == "" || fixture.power == "" || fixture.unsupported == "" {
		t.Fatalf("missing fixture entities: %#v", fixture)
	}
	if _, err = z2m.activateRoutes(
		context.Background(),
		1,
		fixture.connection,
		func(error) {},
		fixture.snapshot,
	); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func invokeRefresh(z2m *Adapter, entityID string, deadline time.Time) adapter.FunctionResult {
	arguments, _ := json.Marshal(map[string]string{"entity_id": entityID})
	return z2m.Functions()[0].Handler(
		context.Background(),
		adapter.FunctionRequest{FunctionKey: "refresh_entity", Arguments: arguments, Deadline: deadline},
	)
}

func awaitRefresh(t *testing.T, results <-chan adapter.FunctionResult) adapter.FunctionResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatal("refresh did not complete")
		return nil
	}
}
