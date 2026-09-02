package zigbee2mqtt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appzigbee2mqtt "github.com/mholtzscher/hearth/internal/app/zigbee2mqtt"
)

func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()

	value, err := appzigbee2mqtt.LoadConfig(filepath.Join("..", "..", "..", "configs", "zigbee2mqtt.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.AdapterID != "zigbee2mqtt" {
		t.Fatalf("adapter_id = %q, want zigbee2mqtt", value.AdapterID)
	}
	if value.MQTT.URL != "tcp://127.0.0.1:1883" {
		t.Fatalf("mqtt.url = %q, want tcp://127.0.0.1:1883", value.MQTT.URL)
	}
	if value.MQTT.BaseTopic != "zigbee2mqtt" {
		t.Fatalf("mqtt.base_topic = %q, want zigbee2mqtt", value.MQTT.BaseTopic)
	}
}

// This test protects route-safe adapter and base-topic names and fails if either
// field stops using the shared Hearth slug validator.
func TestConfigValidateRequiresSlugs(t *testing.T) {
	t.Parallel()

	valid := validConfig()
	valid.AdapterID = strings.Repeat("a", 63)
	valid.MQTT.BaseTopic = strings.Repeat("b", 63)
	if err := valid.Validate(); err != nil {
		t.Fatalf("63-character slugs rejected: %v", err)
	}

	tests := []struct {
		name      string
		configure func(*appzigbee2mqtt.Config)
		field     string
	}{
		{
			name: "empty adapter ID",
			configure: func(value *appzigbee2mqtt.Config) {
				value.AdapterID = ""
			},
			field: "adapter_id",
		},
		{
			name: "uppercase adapter ID",
			configure: func(value *appzigbee2mqtt.Config) {
				value.AdapterID = "Zigbee2mqtt"
			},
			field: "adapter_id",
		},
		{
			name: "base topic with slash",
			configure: func(value *appzigbee2mqtt.Config) {
				value.MQTT.BaseTopic = "house/zigbee2mqtt"
			},
			field: "mqtt.base_topic",
		},
		{
			name: "base topic with MQTT wildcard",
			configure: func(value *appzigbee2mqtt.Config) {
				value.MQTT.BaseTopic = "zigbee+"
			},
			field: "mqtt.base_topic",
		},
		{
			name: "base topic longer than 63 characters",
			configure: func(value *appzigbee2mqtt.Config) {
				value.MQTT.BaseTopic = strings.Repeat("z", 64)
			},
			field: "mqtt.base_topic",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := validConfig()
			test.configure(&value)
			err := value.Validate()
			if err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), test.field) {
				t.Fatalf("Validate() error = %q, want field %q", err, test.field)
			}
		})
	}
}

// This test protects the native NATS connection requirement and fails if the
// application config stops delegating to the existing nats:// validator.
func TestConfigValidateRequiresNATSURL(t *testing.T) {
	t.Parallel()

	value := validConfig()
	value.NATSURL = "http://127.0.0.1:4222"
	if err := value.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-NATS URL")
	}
}

// These examples protect the plain, explicit MQTT endpoint boundary and fail
// if credentials, ambiguous endpoints, or unsupported URL components leak in.
func TestConfigValidateMQTTURL(t *testing.T) {
	t.Parallel()

	validURLs := []string{
		"mqtt://127.0.0.1:1883",
		"tcp://broker.internal:1883",
		"tcp://[::1]:65535",
	}
	for _, mqttURL := range validURLs {
		t.Run("accept "+mqttURL, func(t *testing.T) {
			t.Parallel()
			value := validConfig()
			value.MQTT.URL = mqttURL
			if err := value.Validate(); err != nil {
				t.Fatalf("Validate() rejected %q: %v", mqttURL, err)
			}
		})
	}

	invalidURLs := []string{
		"127.0.0.1:1883",
		"http://127.0.0.1:1883",
		"mqtt://:1883",
		"mqtt://127.0.0.1",
		"mqtt://127.0.0.1:not-a-port",
		"mqtt://127.0.0.1:0",
		"mqtt://127.0.0.1:65536",
		"mqtt://user@127.0.0.1:1883",
		"mqtt://user:password@127.0.0.1:1883",
		"mqtt://127.0.0.1:1883/",
		"mqtt://127.0.0.1:1883/zigbee2mqtt",
		"mqtt://127.0.0.1:1883?client=hearth",
		"mqtt://127.0.0.1:1883?",
		"mqtt://127.0.0.1:1883#connection",
		"mqtt://127.0.0.1:1883#",
	}
	for _, mqttURL := range invalidURLs {
		t.Run("reject "+mqttURL, func(t *testing.T) {
			t.Parallel()
			value := validConfig()
			value.MQTT.URL = mqttURL
			if err := value.Validate(); err == nil {
				t.Fatalf("Validate() accepted %q", mqttURL)
			}
		})
	}
}

// This test protects Paho transport compatibility and fails if the accepted
// mqtt:// alias reaches runtime without becoming tcp://.
func TestLoadConfigNormalizesMQTTURL(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, `adapter_id: zigbee2mqtt
nats_url: nats://127.0.0.1:4222
mqtt:
  url: mqtt://127.0.0.1:1883
  base_topic: zigbee2mqtt
`)
	value, err := appzigbee2mqtt.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if value.MQTT.URL != "tcp://127.0.0.1:1883" {
		t.Fatalf("mqtt.url = %q, want tcp://127.0.0.1:1883", value.MQTT.URL)
	}
}

// This test protects the no-secrets and derived-client-ID contract and fails
// if either unsupported field is accidentally added to static YAML.
func TestLoadConfigRejectsSecretAndClientIDFields(t *testing.T) {
	t.Parallel()

	fields := []string{
		"  username: operator\n",
		"  password: secret\n",
		"  client_id: manually-chosen\n",
	}
	for _, field := range fields {
		t.Run(strings.TrimSpace(field), func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, `adapter_id: zigbee2mqtt
nats_url: nats://127.0.0.1:4222
mqtt:
  url: tcp://127.0.0.1:1883
  base_topic: zigbee2mqtt
`+field)
			if _, err := appzigbee2mqtt.LoadConfig(path); err == nil {
				t.Fatalf("LoadConfig() accepted unsupported field %q", strings.TrimSpace(field))
			}
		})
	}
}

// The hard-coded SHA-256 prefixes independently protect stable, 23-character
// broker client IDs and fail on truncation, casing, input, or prefix mistakes.
func TestDeriveClientID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		adapterID string
		want      string
	}{
		{adapterID: "zigbee2mqtt", want: "hearth-z2m-70f9d81cd993"},
		{adapterID: "zigbee2mqtt-office", want: "hearth-z2m-2cec17258a46"},
		{adapterID: "adapter-a", want: "hearth-z2m-1d362d4f0b7d"},
		{adapterID: "adapter-b", want: "hearth-z2m-7ffc6965d26a"},
	}

	seen := make(map[string]string, len(tests))
	for _, test := range tests {
		clientID := appzigbee2mqtt.DeriveClientID(test.adapterID)
		if clientID != test.want {
			t.Errorf("DeriveClientID(%q) = %q, want %q", test.adapterID, clientID, test.want)
		}
		if len(clientID) != 23 {
			t.Errorf("len(DeriveClientID(%q)) = %d, want 23", test.adapterID, len(clientID))
		}
		if prior, exists := seen[clientID]; exists {
			t.Errorf("adapter IDs %q and %q derived the same client ID %q", prior, test.adapterID, clientID)
		}
		seen[clientID] = test.adapterID
	}
}

func validConfig() appzigbee2mqtt.Config {
	return appzigbee2mqtt.Config{
		AdapterID: "zigbee2mqtt",
		NATSURL:   "nats://127.0.0.1:4222",
		MQTT: appzigbee2mqtt.MQTTConfig{
			URL:       "tcp://127.0.0.1:1883",
			BaseTopic: "zigbee2mqtt",
		},
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
