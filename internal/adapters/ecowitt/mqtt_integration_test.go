package ecowitt //nolint:testpackage // The Paho integration test drives the package-private transport seam.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/mholtzscher/hearth/internal/testbroker"
)

// TestPahoDialerAgainstMosquitto protects the real Paho MQTT 3.1.1 contract
// against a real Mosquitto broker: the exact-topic QoS 1 subscription, retained
// metadata, copied payload ownership, monotonic local receipt times, bounded
// relay overflow, clean-session reconnect, and connection-loss reporting.
//
//nolint:gocognit // Table-driven contract test; the explicit field comparisons are the assertion.
func TestPahoDialerAgainstMosquitto(t *testing.T) {
	t.Parallel()

	broker := testbroker.StartMosquitto(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	publisher := newTestPublisher(t, broker.URL())
	const retainedPayload = "PASSKEY=0123456789abcdef0123456789abcdef&stationtype=GW2000B_V3.3.2&dateutc=2026-09-12+14%3A30%3A00&tempf=65.84"
	if token := publisher.Publish(fixtureTopic, 1, true, retainedPayload); !token.WaitTimeout(time.Second) {
		t.Fatalf("publish retained report: %v", token.Error())
	}

	received := make(chan mqttMessage, 2*mqttRelayCapacity)
	dialer := newPahoDialer()
	subscriber, err := dialer.Dial(
		ctx,
		mqttConfig{URL: broker.URL(), ClientID: "hearth-eco-integration-sub"},
		func(message mqttMessage) { received <- message },
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(subscriber.Close)
	if err = subscriber.Subscribe(ctx, fixtureTopic, mqttQoS); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	retained := receiveMQTTMessage(ctx, t, received)
	if retained.Topic != fixtureTopic {
		t.Fatalf("retained topic = %q, want the exact configured topic", retained.Topic)
	}
	if !retained.Retained {
		t.Fatal("retained delivery was not marked retained")
	}
	if string(retained.Payload) != retainedPayload {
		t.Fatalf("retained payload = %q, want the published bytes", retained.Payload)
	}
	if !hasMonotonic(retained.ReceivedAt) {
		t.Fatal("receipt time lost its Go monotonic reading")
	}
	if time.Since(retained.ReceivedAt) > time.Minute {
		t.Fatalf("receipt time %s is not local receipt time", retained.ReceivedAt)
	}

	livePayload := "PASSKEY=0123456789abcdef0123456789abcdef&stationtype=GW2000B_V3.3.2&dateutc=2026-09-12+14%3A30%3A08&tempf=65.85"
	if token := publisher.Publish(fixtureTopic, 1, false, livePayload); !token.WaitTimeout(time.Second) {
		t.Fatalf("publish live report: %v", token.Error())
	}
	live := receiveMQTTMessage(ctx, t, received)
	if live.Retained {
		t.Fatal("a live delivery was marked retained")
	}
	if string(live.Payload) != livePayload {
		t.Fatalf("live payload = %q, want the published bytes", live.Payload)
	}

	// A blocked consumer must saturate the bounded relay and end the generation
	// rather than dropping a report while the connection stays healthy.
	blockedGate := make(chan struct{})
	blocked, err := dialer.Dial(
		ctx,
		mqttConfig{URL: broker.URL(), ClientID: "hearth-eco-integration-blocked"},
		func(mqttMessage) { <-blockedGate },
	)
	if err != nil {
		t.Fatalf("dial blocked subscriber: %v", err)
	}
	t.Cleanup(blocked.Close)
	if err = blocked.Subscribe(ctx, fixtureTopic, mqttQoS); err != nil {
		t.Fatalf("subscribe blocked subscriber: %v", err)
	}
	for index := range 2 * mqttRelayCapacity {
		token := publisher.Publish(fixtureTopic, 1, false, fmt.Sprintf("blocked-%03d", index))
		if !token.WaitTimeout(time.Second) {
			t.Fatalf("publish blocked message %d: %v", index, token.Error())
		}
	}
	select {
	case lostErr := <-blocked.Lost():
		if !errors.Is(lostErr, errMQTTRelayOverflow) {
			t.Fatalf("blocked consumer loss = %v, want relay overflow", lostErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a saturated real-broker subscription never reported overflow")
	}
	close(blockedGate)

	// Clean-session reconnect: no stale queued messages are replayed, but a
	// retained publication is redelivered on the new subscription.
	reconnectedMessages := make(chan mqttMessage, mqttRelayCapacity)
	reconnected, err := dialer.Dial(
		ctx,
		mqttConfig{URL: broker.URL(), ClientID: "hearth-eco-integration-sub"},
		func(message mqttMessage) { reconnectedMessages <- message },
	)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	t.Cleanup(reconnected.Close)
	select {
	case message := <-reconnectedMessages:
		t.Fatalf("clean session replayed a stale message before SUBACK: %#v", message)
	case <-time.After(100 * time.Millisecond):
	}
	if err = reconnected.Subscribe(ctx, fixtureTopic, mqttQoS); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
	replay := receiveMQTTMessage(ctx, t, reconnectedMessages)
	if !replay.Retained || string(replay.Payload) != retainedPayload {
		t.Fatalf("resubscribe replay = %#v, want the retained publication", replay)
	}

	// Interrupting the broker must surface a connection loss.
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

// newTestPublisher builds a plain Paho publisher for a test broker.
func newTestPublisher(t *testing.T, brokerURL string) paho.Client {
	t.Helper()
	options := paho.NewClientOptions().
		AddBroker(normalizePahoBrokerURL(brokerURL)).
		SetClientID("hearth-eco-integration-pub").
		SetProtocolVersion(mqttProtocolVersion311).
		SetCleanSession(true).
		SetAutoReconnect(false).
		SetConnectRetry(false)
	publisher := paho.NewClient(options)
	if token := publisher.Connect(); !token.WaitTimeout(10 * time.Second) {
		t.Fatalf("connect test publisher: %v", token.Error())
	}
	t.Cleanup(func() {
		publisher.Disconnect(0)
	})
	return publisher
}

// receiveMQTTMessage waits for one delivery or fails.
func receiveMQTTMessage(ctx context.Context, t *testing.T, messages <-chan mqttMessage) mqttMessage {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-ctx.Done():
		t.Fatalf("waiting for an MQTT delivery: %v", ctx.Err())
		return mqttMessage{}
	}
}
