package zigbee2mqtt //nolint:testpackage // Tests exercise the private MQTT transport seam.

import (
	"context"
	"errors"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

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

type stalledPahoToken struct{}

func (stalledPahoToken) Wait() bool { return false }

func (stalledPahoToken) WaitTimeout(time.Duration) bool { return false }

func (stalledPahoToken) Done() <-chan struct{} { return make(chan struct{}) }

func (stalledPahoToken) Error() error { return nil }

var _ paho.Token = stalledPahoToken{}
