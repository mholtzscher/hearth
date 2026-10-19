package zigbee2mqtt //nolint:testpackage // Real broker assertions support the process-level refresh test.

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// MQTT 3 live delivery clears the retain flag even for retained publications.
// A fresh subscription plus an ordered broker barrier proves the get was not
// retained, rather than trusting the flag on the live delivery alone.
func assertRefreshNotRetained(ctx context.Context, t *testing.T, brokerURL string) {
	t.Helper()
	client := connectMQTTClient(t, brokerURL, "refresh-retain-probe")
	messages := make(chan paho.Message, 8)
	receive := func(_ paho.Client, message paho.Message) { messages <- message }
	subscribeMQTT(t, client, "zigbee2mqtt/test-light/get", receive)
	subscribeMQTT(t, client, "refresh-probe/barrier", receive)
	publishMQTT(t, client, "refresh-probe/barrier", false, []byte("barrier"))
	select {
	case message := <-messages:
		if message.Topic() != "refresh-probe/barrier" {
			t.Fatalf("refresh get was retained: %s", message.Payload())
		}
	case <-ctx.Done():
		t.Fatal("waiting for retain probe barrier")
	}
}

func assertDisabledTemperatureRefresh(
	ctx context.Context,
	t *testing.T,
	service *testCoreService,
	input adapters.InvokeInput,
	requests <-chan paho.Message,
) {
	t.Helper()
	entities, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(
		entities.Items,
		func(entity devices.EntityWithState) bool { return entity.Entity.TypeID == "hearth.temperature/v1" },
	)
	if index < 0 {
		t.Fatal("read-only temperature Entity was not registered")
	}
	entity := entities.Items[index]
	if _, err = service.SetEntityEnabled(ctx, entity.Entity.ID, false); err != nil {
		t.Fatal(err)
	}
	input.Arguments = json.RawMessage(`{"entity_id":"` + string(entity.Entity.ID) + `"}`)
	invocation, invokeErr := service.functions.Invoke(ctx, input)
	if invokeErr != nil {
		t.Fatal(invokeErr)
	}
	if invocation.Completion == nil {
		t.Fatal("temperature refresh has no completion")
	}
	if _, ok := invocation.Completion.Evidence.(adapters.Succeeded); !ok {
		t.Fatalf("disabled read-only refresh = %#v", invocation.Completion)
	}
	select {
	case message := <-requests:
		if string(message.Payload()) != `{"temperature":""}` || message.Qos() != 1 {
			t.Fatalf("temperature refresh = %s qos=%d", message.Payload(), message.Qos())
		}
	case <-ctx.Done():
		t.Fatal("missing temperature refresh")
	}
	current, readErr := service.GetEntity(ctx, entity.Entity.ID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if current.State != nil || current.Entity.Enabled {
		t.Fatal("refresh fabricated State or enabled the Entity")
	}
}
