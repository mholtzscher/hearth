package zwavejs_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	appzwavejs "github.com/mholtzscher/hearth/internal/app/zwavejs"
)

// This test protects graceful startup cancellation and fails if a canceled
// startup context is converted into a generic Run error instead of propagating
// context cancellation for the executable wrapper to report as process.stopped.
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

// This test protects the assembly order and fails if Run connects to NATS or
// dials the Z-Wave JS server before strictly validating the trusted-endpoint
// configuration. A rejected endpoint must never be reached over the network.
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
