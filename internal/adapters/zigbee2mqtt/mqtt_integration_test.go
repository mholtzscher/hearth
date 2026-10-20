//go:build integration

package zigbee2mqtt //nolint:testpackage // Tests exercise the private MQTT transport seam.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/mosquitto/mosquittotest"
)

// TestPahoDialerAgainstMosquitto protects protocol-compatible QoS 1 traffic,
// retained metadata, loss reporting, and the no-drop relay. It fails if Paho is
// misconfigured for a real MQTT broker or a blocked consumer stalls or drops
// callbacks. It owns its Mosquitto container so it can end the broker mid-test.
//
//nolint:gocognit // One broker lifecycle proves retained, clean-session, backpressure, and loss behavior.
func TestPahoDialerAgainstMosquitto(t *testing.T) {
	t.Parallel()

	broker := mosquittotest.StartMosquitto(t)
	brokerURL := broker.URL()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dialer := newPahoDialer()
	publisher, dialErr := dialer.Dial(
		ctx,
		mqttConfig{URL: brokerURL, ClientID: "hearth-z2m-publisher"},
		func(mqttMessage) {},
	)
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	t.Cleanup(publisher.Close)

	const retainedTopic = "zigbee2mqtt/bridge/state"
	publishErr := publisher.Publish(ctx, retainedTopic, 1, true, []byte(`{"state":"online"}`))
	if publishErr != nil {
		t.Fatal(publishErr)
	}

	received := make(chan mqttMessage)
	subscriber, dialErr := dialer.Dial(
		ctx,
		mqttConfig{URL: brokerURL, ClientID: "hearth-z2m-subscriber"},
		func(message mqttMessage) {
			received <- message
		},
	)
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	t.Cleanup(subscriber.Close)
	subscribeErr := subscriber.Subscribe(ctx, "zigbee2mqtt/#", 1)
	if subscribeErr != nil {
		t.Fatal(subscribeErr)
	}

	retained := receiveMQTTMessage(ctx, t, received)
	if retained.Topic != retainedTopic {
		t.Fatalf("retained topic = %q, want %q", retained.Topic, retainedTopic)
	}
	if string(retained.Payload) != `{"state":"online"}` {
		t.Fatalf("retained payload = %q", retained.Payload)
	}
	if !retained.Retained {
		t.Fatal("retained delivery was not marked retained")
	}
	if retained.ReceivedAt.Location() != time.UTC {
		t.Fatalf("receive timestamp location = %v, want UTC", retained.ReceivedAt.Location())
	}

	const messageCount = 128
	for index := range messageCount {
		payload := []byte(fmt.Sprintf("message-%03d", index))
		burstPublishErr := publisher.Publish(ctx, "zigbee2mqtt/office-table-lamp", 1, false, payload)
		if burstPublishErr != nil {
			t.Fatalf("publish message %d: %v", index, burstPublishErr)
		}
	}
	for index := range messageCount {
		message := receiveMQTTMessage(ctx, t, received)
		want := fmt.Sprintf("message-%03d", index)
		if string(message.Payload) != want {
			t.Fatalf("message %d payload = %q, want %q", index, message.Payload, want)
		}
		if message.Retained {
			t.Fatalf("message %d was unexpectedly retained", index)
		}
	}

	subscriber.Close()
	if err := publisher.Publish(ctx, "zigbee2mqtt/missed", 1, false, []byte("not-durable")); err != nil {
		t.Fatal(err)
	}
	reconnectedMessages := make(chan mqttMessage, 1)
	reconnected, dialErr := dialer.Dial(
		ctx,
		mqttConfig{URL: brokerURL, ClientID: "hearth-z2m-subscriber"},
		func(message mqttMessage) { reconnectedMessages <- message },
	)
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	t.Cleanup(reconnected.Close)
	select {
	case message := <-reconnectedMessages:
		t.Fatalf("clean-session reconnect received stale message: %#v", message)
	case <-time.After(100 * time.Millisecond):
	}
	if err := reconnected.Subscribe(ctx, "zigbee2mqtt/#", 1); err != nil {
		t.Fatal(err)
	}
	replayed := receiveMQTTMessage(ctx, t, reconnectedMessages)
	if !replayed.Retained || replayed.Topic != retainedTopic {
		t.Fatalf("retained replay after resubscribe = %#v", replayed)
	}
	if err := publisher.Publish(ctx, "zigbee2mqtt/reconnected", 1, false, []byte("live")); err != nil {
		t.Fatal(err)
	}
	if message := receiveMQTTMessage(ctx, t, reconnectedMessages); string(message.Payload) != "live" {
		t.Fatalf("reconnected payload = %q, want live", message.Payload)
	}

	broker.Stop()
	select {
	case lostErr := <-reconnected.Lost():
		if lostErr == nil {
			t.Fatal("connection loss error is nil")
		}
	case <-ctx.Done():
		t.Fatalf("waiting for connection loss: %v", ctx.Err())
	}
}

func receiveMQTTMessage(ctx context.Context, t *testing.T, messages <-chan mqttMessage) mqttMessage {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-ctx.Done():
		t.Fatalf("waiting for MQTT message: %v", ctx.Err())
		return mqttMessage{}
	}
}
