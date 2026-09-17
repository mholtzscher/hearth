package zigbee2mqtt //nolint:testpackage // Tests exercise the private MQTT transport seam.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

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

// TestPahoClientOptions protects the MQTT 3.1.1 clean-session, one-connection
// lifecycle contract. It fails if a Paho default silently enables reconnects or
// changes the wire protocol.
func TestPahoClientOptions(t *testing.T) {
	t.Parallel()

	options := newPahoClientOptions(mqttConfig{
		URL:      "mqtt://127.0.0.1:1883",
		ClientID: "hearth-z2m-options",
	}, func(error) {})

	if len(options.Servers) != 1 || options.Servers[0].String() != "tcp://127.0.0.1:1883" {
		t.Fatalf("servers = %v, want one normalized tcp URL", options.Servers)
	}
	if options.ClientID != "hearth-z2m-options" {
		t.Fatalf("client ID = %q", options.ClientID)
	}
	if options.ProtocolVersion != 4 {
		t.Fatalf("protocol version = %d, want MQTT 3.1.1 version 4", options.ProtocolVersion)
	}
	if !options.CleanSession {
		t.Fatal("clean session is disabled")
	}
	if options.AutoReconnect {
		t.Fatal("automatic reconnect is enabled")
	}
	if options.ConnectRetry {
		t.Fatal("initial connect retry is enabled")
	}
	if !options.Order {
		t.Fatal("ordered callback ingestion is disabled")
	}
}

// TestWaitPahoTokenHonorsContext protects bounded Paho token waits. It fails if
// connect, subscribe, or publish can wait indefinitely after context expiry.
func TestWaitPahoTokenHonorsContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitPahoToken(ctx, stalledPahoToken{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context cancellation", err)
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

type stalledPahoToken struct{}

func (stalledPahoToken) Wait() bool                     { return false }
func (stalledPahoToken) WaitTimeout(time.Duration) bool { return false }
func (stalledPahoToken) Done() <-chan struct{}          { return make(chan struct{}) }
func (stalledPahoToken) Error() error                   { return nil }

var _ paho.Token = stalledPahoToken{}
