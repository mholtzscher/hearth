package ecowitt_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	appecowitt "github.com/mholtzscher/hearth/internal/app/ecowitt"
)

// This test protects graceful startup cancellation and fails if a canceled
// startup context is converted into a generic Run error instead of propagating
// context cancellation for the executable wrapper to report as process.stopped.
func TestRunCanceledContextReturnsCancellation(t *testing.T) {
	t.Parallel()
	passkeyFile := filepath.Join(t.TempDir(), "passkey")
	if err := os.WriteFile(passkeyFile, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := appecowitt.Config{
		AdapterID: "ecowitt-test",
		NATSURL:   "nats://127.0.0.1:4222",
		MQTT: appecowitt.MQTTConfig{
			URL:   "mqtt://127.0.0.1:1883",
			Topic: "ecowitt/943cc64457a7",
		},
		Station: appecowitt.StationConfig{
			GatewayName:           "Weather Station Gateway",
			OutdoorArrayName:      "Outdoor Weather Array",
			PasskeyFile:           passkeyFile,
			UploadIntervalSeconds: 16,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := appecowitt.Run(ctx, config, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context = %v, want context.Canceled", err)
	}
}
