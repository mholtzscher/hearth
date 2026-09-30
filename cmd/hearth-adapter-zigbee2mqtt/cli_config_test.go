package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/zigbee2mqtt"
)

func TestEnvAndFlagOverrideYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "adapter_id: zigbee2mqtt\nnats_url: nats://127.0.0.1:4222\nmqtt: {url: tcp://127.0.0.1:1883, base_topic: zigbee2mqtt}\nfuture: ignored\n"
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_ADAPTER_ZIGBEE2MQTT_MQTT_BASE_TOPIC", "from_env")
	got := invoke(t, []string{"--config", p})
	if got.MQTT.BaseTopic != "from_env" {
		t.Fatalf("env override = %q", got.MQTT.BaseTopic)
	}
	got = invoke(t, []string{"--config", p, "--mqtt-base-topic", "from_flag"})
	if got.MQTT.BaseTopic != "from_flag" {
		t.Fatalf("flag override = %q", got.MQTT.BaseTopic)
	}
}
func TestMQTTOverrideNormalizedBeforeRun(t *testing.T) {
	t.Parallel()
	got := invoke(
		t,
		[]string{
			"--adapter-id",
			"zigbee2mqtt",
			"--nats-url",
			"nats://127.0.0.1:4222",
			"--mqtt-url",
			"mqtt://127.0.0.1:1883",
			"--mqtt-base-topic",
			"zigbee2mqtt",
		},
	)
	if got.MQTT.URL != "tcp://127.0.0.1:1883" {
		t.Fatalf("mqtt.url = %q", got.MQTT.URL)
	}
}

func invoke(t *testing.T, args []string) zigbee2mqtt.Config {
	t.Helper()
	var got zigbee2mqtt.Config
	c := newCommand(
		func(_ context.Context, v zigbee2mqtt.Config, _ *slog.Logger) error { got = v; return nil },
		io.Discard,
	)
	if err := c.Run(context.Background(), append([]string{"app"}, args...)); err != nil {
		t.Fatal(err)
	}
	return got
}
