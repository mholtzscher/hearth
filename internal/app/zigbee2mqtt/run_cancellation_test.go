package zigbee2mqtt_test

import (
	"context"
	"errors"
	"testing"

	appzigbee2mqtt "github.com/mholtzscher/hearth/internal/app/zigbee2mqtt"
)

// This test protects graceful startup cancellation and fails if a canceled
// startup context is converted into a generic Run error instead of
// propagating context cancellation for the executable wrapper to report as
// process.stopped.
func TestRunCanceledContextReturnsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := appzigbee2mqtt.Config{
		AdapterID: "zigbee2mqtt-test", NATSURL: "nats://127.0.0.1:4222",
		MQTT: appzigbee2mqtt.MQTTConfig{
			URL: "mqtt://127.0.0.1:1883", BaseTopic: "zigbee2mqtt",
		},
	}
	if err := appzigbee2mqtt.Run(ctx, config, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context = %v, want context.Canceled", err)
	}
}
