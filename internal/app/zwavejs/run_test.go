package zwavejs_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	appzwavejs "github.com/mholtzscher/hearth/internal/app/zwavejs"
)

// Run propagates startup cancellation so the executable can report process.stopped.
func TestRunCanceledContextReturnsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := appzwavejs.Config{
		AdapterID: "zwavejs-test",
		NATSURL:   "nats://127.0.0.1:4222",
		ZWaveJS:   appzwavejs.ZWaveJSConfig{URL: "ws://127.0.0.1:3000"},
	}
	if err := appzwavejs.Run(ctx, config, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context = %v, want context.Canceled", err)
	}
}

// Run validates trusted endpoints before connecting to NATS or Z-Wave JS. Rejected endpoints must never be reached over
// the network.
func TestRunRejectsUntrustedEndpointBeforeConnecting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
	}{
		{"tls", "wss://127.0.0.1:3000"},
		{"credentials", "ws://operator:secret@127.0.0.1:3000"},
		{"path", "ws://127.0.0.1:3000/zwave"},
		{"missing port", "ws://127.0.0.1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := appzwavejs.Config{
				AdapterID: "zwavejs-test",
				NATSURL:   "nats://127.0.0.1:4222",
				ZWaveJS:   appzwavejs.ZWaveJSConfig{URL: test.url},
			}
			err := appzwavejs.Run(t.Context(), config, nil)
			if err == nil || !strings.Contains(err.Error(), "zwave_js.url") {
				t.Fatalf("Run error = %v, want a zwave_js.url validation failure", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("Run error repeated rejected credentials: %v", err)
			}
		})
	}
}
