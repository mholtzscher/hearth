package ecowitt_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appecowitt "github.com/mholtzscher/hearth/internal/app/ecowitt"
)

// validPasskeyHex is the sanitized fixture PASSKEY. It is not a real secret.
const validPasskeyHex = "0123456789abcdef0123456789abcdef"

// This test protects the checked-in example and fails if it stops documenting
// loopback or fake station values or drifts from the accepted field shape.
func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()
	example, err := os.ReadFile(filepath.Join("..", "..", "..", "configs", "ecowitt.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// The example intentionally points at a deployment secret path, so the
	// station identity fields are verified against a temporary PASSKEY file.
	contents := strings.Replace(
		string(example), "/run/secrets/ecowitt-passkey", writePasskeyFile(t, validPasskeyHex), 1,
	)
	value, err := appecowitt.LoadConfig(writeConfig(t, contents))
	if err != nil {
		t.Fatal(err)
	}
	if value.AdapterID != "ecowitt" {
		t.Fatalf("adapter_id = %q, want ecowitt", value.AdapterID)
	}
	if value.MQTT.URL != "tcp://127.0.0.1:1883" {
		t.Fatalf("mqtt.url = %q, want tcp://127.0.0.1:1883", value.MQTT.URL)
	}
	if value.MQTT.Topic != "ecowitt/943cc64457a7" {
		t.Fatalf("mqtt.topic = %q", value.MQTT.Topic)
	}
	if value.Station.GatewayName != "Weather Station Gateway" ||
		value.Station.OutdoorArrayName != "Outdoor Weather Array" {
		t.Fatalf("station names = %#v", value.Station)
	}
	if value.Station.UploadIntervalSeconds != 16 {
		t.Fatalf("upload_interval_seconds = %d", value.Station.UploadIntervalSeconds)
	}
}

// This test protects the route-safe adapter slug and fails if adapter_id stops
// using the shared Hearth slug validator.
func TestConfigValidateRequiresSlugs(t *testing.T) {
	t.Parallel()
	valid := validConfig(t)
	valid.AdapterID = strings.Repeat("a", 63)
	if err := valid.Validate(); err != nil {
		t.Fatalf("63-character adapter_id rejected: %v", err)
	}
	for _, adapterID := range []string{"", "Ecowitt", "contains.period", "contains/slash"} {
		t.Run("adapter "+adapterID, func(t *testing.T) {
			t.Parallel()
			value := validConfig(t)
			value.AdapterID = adapterID
			assertValidationError(t, value, "adapter_id")
		})
	}
}

// This test protects the native NATS connection requirement and fails if the
// application config stops delegating to the existing nats:// validator.
func TestConfigValidateRequiresNATSURL(t *testing.T) {
	t.Parallel()
	value := validConfig(t)
	value.NATSURL = "http://127.0.0.1:4222"
	assertValidationError(t, value, "nats_url")
}

// These examples protect the plain, explicit MQTT endpoint boundary and fail
// if credentials, TLS schemes, ambiguous endpoints, or unsupported URL
// components leak in.
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
			value := validConfig(t)
			value.MQTT.URL = mqttURL
			if err := value.Validate(); err != nil {
				t.Fatalf("Validate() rejected %q: %v", mqttURL, err)
			}
		})
	}

	invalidURLs := []string{
		"",
		"127.0.0.1:1883",
		"http://127.0.0.1:1883",
		"mqtts://127.0.0.1:8883",
		"ssl://127.0.0.1:8883",
		"mqtt://:1883",
		"mqtt://127.0.0.1",
		"mqtt://127.0.0.1:not-a-port",
		"mqtt://127.0.0.1:0",
		"mqtt://127.0.0.1:65536",
		"mqtt://user@127.0.0.1:1883",
		"mqtt://user:password@127.0.0.1:1883",
		"mqtt://127.0.0.1:1883/",
		"mqtt://127.0.0.1:1883/ecowitt",
		"mqtt://127.0.0.1:1883?client=hearth",
		"mqtt://127.0.0.1:1883?",
		"mqtt://127.0.0.1:1883#connection",
		"MQTT://127.0.0.1:1883",
		"Tcp://127.0.0.1:1883",
		"TCP://127.0.0.1:1883",
	}
	for _, mqttURL := range invalidURLs {
		t.Run("reject "+mqttURL, func(t *testing.T) {
			t.Parallel()
			value := validConfig(t)
			value.MQTT.URL = mqttURL
			assertValidationError(t, value, "mqtt.url")
		})
	}
}

// This test protects the case-sensitive scheme contract and fails if any
// case-variant mqtt:// or tcp:// scheme passes Config.Validate or LoadConfig
// only to be rejected later by the MQTT transport. url.Parse lowercases
// parsed.Scheme, so the raw prefix must be checked independently.
func TestConfigValidateRejectsCaseVariantMQTTSchemes(t *testing.T) {
	t.Parallel()
	for _, scheme := range []string{"mqtt", "tcp"} {
		for _, variant := range caseVariants(scheme) {
			if variant == scheme {
				continue
			}
			mqttURL := variant + "://127.0.0.1:1883"
			t.Run("validate "+variant, func(t *testing.T) {
				t.Parallel()
				value := validConfig(t)
				value.MQTT.URL = mqttURL
				assertValidationError(t, value, "mqtt.url")
			})
			t.Run("load "+variant, func(t *testing.T) {
				t.Parallel()
				path := writeConfig(t, validConfigYAML(t, mqttURL, "ecowitt/943cc64457a7"))
				if _, err := appecowitt.LoadConfig(path); err == nil {
					t.Fatalf("LoadConfig() accepted case-variant scheme %q", variant+"://")
				}
			})
		}
	}
}

// caseVariants returns every letter-case combination of one lowercase ASCII
// word, including the original word itself.
func caseVariants(word string) []string {
	variants := []string{""}
	for _, r := range word {
		next := make([]string, 0, len(variants)*2)
		for _, prefix := range variants {
			next = append(next, prefix+string(r), prefix+strings.ToUpper(string(r)))
		}
		variants = next
	}
	return variants
}

// This test protects the one exact-topic subscription and fails if a wildcard,
// ambiguous, uppercase, or over-long topic is accepted.
func TestConfigValidateMQTTTopic(t *testing.T) {
	t.Parallel()
	validTopics := []string{
		"ecowitt/943cc64457a7",
		"a/b",
		"station-1/array_2",
		strings.Repeat("a", 63) + "/" + strings.Repeat("b", 63),
	}
	for _, topic := range validTopics {
		t.Run("accept "+topic, func(t *testing.T) {
			t.Parallel()
			value := validConfig(t)
			value.MQTT.Topic = topic
			if err := value.Validate(); err != nil {
				t.Fatalf("Validate() rejected %q: %v", topic, err)
			}
		})
	}

	invalidTopics := []string{
		"",
		"ecowitt",
		"ecowitt/",
		"/ecowitt",
		"ecowitt/station/extra",
		"ecowitt/#",
		"ecowitt/+",
		"#",
		"+",
		"Ecowitt/station",
		"ecowitt/Station",
		"-ecowitt/station",
		"ecowitt/-station",
		"ecowitt/station topic",
		strings.Repeat("a", 64) + "/station",
		"ecowitt/" + strings.Repeat("b", 64),
	}
	for _, topic := range invalidTopics {
		t.Run("reject "+topic, func(t *testing.T) {
			t.Parallel()
			value := validConfig(t)
			value.MQTT.Topic = topic
			assertValidationError(t, value, "mqtt.topic")
		})
	}
}

// This test protects the Device name bounds and fails if a blank or over-long
// configured name is accepted.
func TestConfigValidateDeviceNames(t *testing.T) {
	t.Parallel()
	valid := validConfig(t)
	valid.Station.GatewayName = strings.Repeat("g", 128)
	valid.Station.OutdoorArrayName = " Array "
	if err := valid.Validate(); err != nil {
		t.Fatalf("boundary names rejected: %v", err)
	}

	tests := []struct {
		name      string
		configure func(*appecowitt.Config)
		field     string
	}{
		{
			name:      "gateway blank",
			configure: func(value *appecowitt.Config) { value.Station.GatewayName = "   " },
			field:     "station.gateway_name",
		},
		{
			name:      "gateway too long",
			configure: func(value *appecowitt.Config) { value.Station.GatewayName = strings.Repeat("g", 129) },
			field:     "station.gateway_name",
		},
		{
			name:      "outdoor array blank",
			configure: func(value *appecowitt.Config) { value.Station.OutdoorArrayName = "" },
			field:     "station.outdoor_array_name",
		},
		{
			name:      "outdoor array too long",
			configure: func(value *appecowitt.Config) { value.Station.OutdoorArrayName = strings.Repeat("o", 129) },
			field:     "station.outdoor_array_name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := validConfig(t)
			test.configure(&value)
			assertValidationError(t, value, test.field)
		})
	}
}

// This test protects the configured gateway cadence boundary and fails if an
// upload interval outside 8 through 600 seconds is accepted.
func TestConfigValidateUploadInterval(t *testing.T) {
	t.Parallel()
	for _, seconds := range []int{8, 9, 600} {
		t.Run(fmt.Sprintf("accept %d", seconds), func(t *testing.T) {
			t.Parallel()
			value := validConfig(t)
			value.Station.UploadIntervalSeconds = seconds
			if err := value.Validate(); err != nil {
				t.Fatalf("Validate() rejected %d: %v", seconds, err)
			}
		})
	}
	for _, seconds := range []int{0, 7, 601, -1} {
		t.Run(fmt.Sprintf("reject %d", seconds), func(t *testing.T) {
			t.Parallel()
			value := validConfig(t)
			value.Station.UploadIntervalSeconds = seconds
			assertValidationError(t, value, "station.upload_interval_seconds")
		})
	}
}

// This test protects PASSKEY-file handling and fails if the Adapter accepts a
// missing, non-regular, empty, wrong-length, or non-hexadecimal secret.
func TestConfigValidatePasskeyFile(t *testing.T) {
	t.Parallel()
	valid := validConfig(t)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid PASSKEY file rejected: %v", err)
	}

	trailingNewline := writePasskeyFile(t, validPasskeyHex+"\n")
	if _, err := appecowitt.LoadPasskeyFile(trailingNewline); err != nil {
		t.Fatalf("trimmed PASSKEY rejected: %v", err)
	}

	tests := []struct {
		name    string
		prepare func(t *testing.T) string
	}{
		{
			name:    "missing",
			prepare: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") },
		},
		{
			name:    "directory",
			prepare: func(t *testing.T) string { return t.TempDir() },
		},
		{
			name:    "empty",
			prepare: func(t *testing.T) string { return writePasskeyFile(t, "") },
		},
		{
			name:    "short",
			prepare: func(t *testing.T) string { return writePasskeyFile(t, validPasskeyHex[:31]) },
		},
		{
			name:    "long",
			prepare: func(t *testing.T) string { return writePasskeyFile(t, validPasskeyHex+"0") },
		},
		{
			name:    "non hex",
			prepare: func(t *testing.T) string { return writePasskeyFile(t, strings.Repeat("z", 32)) },
		},
		{
			name:    "two lines",
			prepare: func(t *testing.T) string { return writePasskeyFile(t, validPasskeyHex+"\n"+validPasskeyHex) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := validConfig(t)
			value.Station.PasskeyFile = test.prepare(t)
			assertValidationError(t, value, "station.passkey_file")
			if _, err := appecowitt.LoadPasskeyFile(value.Station.PasskeyFile); err == nil {
				t.Fatal("LoadPasskeyFile unexpectedly accepted an invalid file")
			}
		})
	}

	t.Run("empty path", func(t *testing.T) {
		t.Parallel()
		value := validConfig(t)
		value.Station.PasskeyFile = "  "
		assertValidationError(t, value, "station.passkey_file")
	})
}

// This test protects Paho transport compatibility and fails if the accepted
// mqtt:// alias reaches runtime without becoming tcp://.
func TestLoadConfigNormalizesMQTTURL(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, validConfigYAML(t, "mqtt://127.0.0.1:1883", "ecowitt/943cc64457a7"))
	value, err := appecowitt.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if value.MQTT.URL != "tcp://127.0.0.1:1883" {
		t.Fatalf("mqtt.url = %q, want tcp://127.0.0.1:1883", value.MQTT.URL)
	}
}

// This test protects the no-secrets and derived-client-ID contract and fails
// if unsupported fields, including MQTT credentials or a manual client ID, are
// accepted in static YAML.
func TestLoadConfigRejectsUnsupportedFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mqttLine string
	}{
		{name: "top level unknown", mqttLine: ""},
		{name: "mqtt username", mqttLine: "  username: operator\n"},
		{name: "mqtt password", mqttLine: "  password: secret\n"},
		{name: "mqtt client id", mqttLine: "  client_id: manual\n"},
		{name: "mqtt tls", mqttLine: "  tls: true\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, "adapter_id: ecowitt\n"+
				"nats_url: nats://127.0.0.1:4222\n"+
				"mqtt:\n"+
				"  url: tcp://127.0.0.1:1883\n"+
				"  topic: ecowitt/943cc64457a7\n"+
				test.mqttLine+
				"station:\n"+
				"  gateway_name: Gateway\n"+
				"  outdoor_array_name: Array\n"+
				"  passkey_file: "+writePasskeyFile(t, validPasskeyHex)+"\n"+
				"  upload_interval_seconds: 16\n"+
				"unknown: true\n")
			if _, err := appecowitt.LoadConfig(path); err == nil {
				t.Fatalf("LoadConfig() accepted unsupported field %q", test.name)
			}
		})
	}
}

// The hard-coded SHA-256 prefixes independently protect a stable,
// 23-character broker client ID and fail on truncation, casing, prefix, or
// input mistakes.
func TestDeriveClientID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		adapterID string
		want      string
	}{
		{adapterID: "ecowitt", want: "hearth-eco-e96cdbf2d100"},
		{adapterID: "test-ecowitt", want: "hearth-eco-b382f28d6605"},
		{adapterID: "adapter-a", want: "hearth-eco-1d362d4f0b7d"},
		{adapterID: "adapter-b", want: "hearth-eco-7ffc6965d26a"},
	}
	seen := make(map[string]string, len(tests))
	for _, test := range tests {
		clientID := appecowitt.DeriveClientID(test.adapterID)
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

// This test protects A4 and fails if a configuration error repeats the
// configured topic, the PASSKEY file contents, or the PASSKEY value.
func TestValidationErrorsNeverExposeSecrets(t *testing.T) {
	t.Parallel()
	const topicSentinel = "ecowitt/secretmac00"
	const passkeySentinel = "zzsecretzzsecretzzsecretzzsecret"

	passkeyPath := writePasskeyFile(t, validPasskeyHex)
	value := validConfig(t)
	value.MQTT.Topic = topicSentinel
	value.Station.PasskeyFile = passkeyPath
	value.Station.UploadIntervalSeconds = 7
	err := value.Validate()
	if err == nil {
		t.Fatal("Validate() unexpectedly accepted an invalid interval")
	}
	assertNoSentinel(t, err.Error(), topicSentinel, passkeySentinel)

	// A PASSKEY file whose contents are non-hexadecimal must fail without
	// echoing the file contents.
	badPath := writePasskeyFile(t, passkeySentinel)
	value = validConfig(t)
	value.Station.PasskeyFile = badPath
	err = value.Validate()
	if err == nil {
		t.Fatal("Validate() unexpectedly accepted a non-hexadecimal PASSKEY")
	}
	assertNoSentinel(t, err.Error(), topicSentinel, passkeySentinel)
}

// assertValidationError requires one field-specific validation failure.
func assertValidationError(t *testing.T, value appecowitt.Config, field string) {
	t.Helper()
	err := value.Validate()
	if err == nil {
		t.Fatalf("Validate() unexpectedly succeeded for field %q", field)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("Validate() error = %q, want field %q", err, field)
	}
}

// assertNoSentinel fails if any sentinel appears in one diagnostic.
func assertNoSentinel(t *testing.T, message string, sentinels ...string) {
	t.Helper()
	for _, sentinel := range sentinels {
		if strings.Contains(message, sentinel) {
			t.Fatalf("diagnostic %q leaks sentinel %q", message, sentinel)
		}
	}
}

// validConfig builds a configuration with one temporary valid PASSKEY file.
func validConfig(t *testing.T) appecowitt.Config {
	t.Helper()
	return appecowitt.Config{
		AdapterID: "ecowitt",
		NATSURL:   "nats://127.0.0.1:4222",
		MQTT: appecowitt.MQTTConfig{
			URL:   "tcp://127.0.0.1:1883",
			Topic: "ecowitt/943cc64457a7",
		},
		Station: appecowitt.StationConfig{
			GatewayName:           "Weather Station Gateway",
			OutdoorArrayName:      "Outdoor Weather Array",
			PasskeyFile:           writePasskeyFile(t, validPasskeyHex),
			UploadIntervalSeconds: 16,
		},
	}
}

// validConfigYAML renders one complete configuration document.
func validConfigYAML(t *testing.T, mqttURL, topic string) string {
	t.Helper()
	return "adapter_id: ecowitt\n" +
		"nats_url: nats://127.0.0.1:4222\n" +
		"mqtt:\n" +
		"  url: " + mqttURL + "\n" +
		"  topic: " + topic + "\n" +
		"station:\n" +
		"  gateway_name: Weather Station Gateway\n" +
		"  outdoor_array_name: Outdoor Weather Array\n" +
		"  passkey_file: " + writePasskeyFile(t, validPasskeyHex) + "\n" +
		"  upload_interval_seconds: 16\n"
}

// writeConfig writes one YAML document to a temporary file.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writePasskeyFile writes one PASSKEY file to a temporary file.
func writePasskeyFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passkey")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
