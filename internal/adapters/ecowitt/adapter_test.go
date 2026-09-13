package ecowitt //nolint:testpackage // Adapter tests exercise the package-private constructor and runtime.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// recordingResponder is a fake command Responder.
type recordingResponder struct {
	mutex       sync.Mutex
	unavailable []string
	rejected    []string
}

// Accept implements adapter.Responder.
func (*recordingResponder) Accept() (adapter.CommandEvidence, error) {
	return nil, errors.New("unexpected command acceptance")
}

// Reject implements adapter.Responder.
func (responder *recordingResponder) Reject(message string) error {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	responder.rejected = append(responder.rejected, message)
	return nil
}

// RejectUnavailable implements adapter.Responder.
func (responder *recordingResponder) RejectUnavailable(message string) error {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	responder.unavailable = append(responder.unavailable, message)
	return nil
}

// TestNewValidatesConfiguration protects the adapter-package invariants a
// direct caller needs, and that no validation diagnostic repeats the topic,
// the client ID, or the PASSKEY.
func TestNewValidatesConfiguration(t *testing.T) {
	t.Parallel()

	// secretTopic is a distinctive placeholder that can never collide with the
	// fixed validation text, so a leak assertion is meaningful.
	const secretTopic = "zzsecretzz"
	valid := testConfig(t)
	if _, err := newAdapter(&recordingSession{}, valid, nil, newFakeDialer()); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}

	for _, testCase := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "empty broker URL", mutate: func(config *Config) { config.MQTTURL = "" }},
		{name: "unsupported scheme", mutate: func(config *Config) { config.MQTTURL = "https://127.0.0.1:1883" }},
		{name: "tls scheme", mutate: func(config *Config) { config.MQTTURL = "ssl://127.0.0.1:8883" }},
		{name: "empty topic", mutate: func(config *Config) { config.MQTTTopic = "" }},
		{name: "single segment topic", mutate: func(config *Config) { config.MQTTTopic = secretTopic }},
		{name: "three segment topic", mutate: func(config *Config) { config.MQTTTopic = secretTopic + "/a/b" }},
		{name: "multi level wildcard", mutate: func(config *Config) { config.MQTTTopic = secretTopic + "/#" }},
		{name: "single level wildcard", mutate: func(config *Config) { config.MQTTTopic = secretTopic + "/+" }},
		{name: "uppercase topic segment", mutate: func(config *Config) { config.MQTTTopic = secretTopic + "/ABCDEF" }},
		{name: "leading separator topic", mutate: func(config *Config) { config.MQTTTopic = secretTopic + "/-station" }},
		{name: "empty client ID", mutate: func(config *Config) { config.MQTTClientID = " " }},
		{name: "over long client ID", mutate: func(config *Config) { config.MQTTClientID = strings.Repeat("a", 24) }},
		{name: "empty gateway name", mutate: func(config *Config) { config.GatewayName = "   " }},
		{name: "over long gateway name", mutate: func(config *Config) { config.GatewayName = strings.Repeat("a", 129) }},
		{name: "empty outdoor array name", mutate: func(config *Config) { config.OutdoorArrayName = "" }},
		{name: "missing PASSKEY", mutate: func(config *Config) { config.ExpectedPasskey = [16]byte{} }},
		{name: "short interval", mutate: func(config *Config) { config.UploadInterval = 7 * time.Second }},
		{name: "long interval", mutate: func(config *Config) { config.UploadInterval = 601 * time.Second }},
		{name: "missing interval", mutate: func(config *Config) { config.UploadInterval = 0 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			testCase.mutate(&config)
			_, err := newAdapter(&recordingSession{}, config, nil, newFakeDialer())
			if err == nil {
				t.Fatal("invalid configuration was accepted")
			}
			diagnostic := err.Error()
			if strings.Contains(diagnostic, config.MQTTTopic) && config.MQTTTopic != "" {
				t.Fatalf("validation error repeated the topic: %s", diagnostic)
			}
			if strings.Contains(diagnostic, sanitizedPasskeyHex) {
				t.Fatalf("validation error repeated the PASSKEY: %s", diagnostic)
			}
		})
	}

	if _, err := newAdapter(nil, valid, nil, newFakeDialer()); err == nil {
		t.Fatal("a nil Session was accepted")
	}
	if _, err := newAdapter(&recordingSession{}, valid, nil, nil); err == nil {
		t.Fatal("a nil MQTT dialer was accepted")
	}
}

// TestNewEnforcesExactBrokerURL protects the direct package caller boundary: the
// broker URL must be an absolute lowercase mqtt:// or tcp:// URL with an
// explicit host and port and no user info, path, query, fragment, or TLS
// scheme. Rejections never repeat the URL.
func TestNewEnforcesExactBrokerURL(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		url   string
		valid bool
	}{
		{name: "mqtt scheme", url: "mqtt://127.0.0.1:1883", valid: true},
		{name: "tcp scheme", url: "tcp://127.0.0.1:1883", valid: true},
		{name: "hostname", url: "tcp://broker.internal:1883", valid: true},
		{name: "empty", url: ""},
		{name: "uppercase scheme", url: "MQTT://127.0.0.1:1883"},
		{name: "mixed case scheme", url: "Tcp://127.0.0.1:1883"},
		{name: "tls scheme", url: "mqtts://127.0.0.1:8883"},
		{name: "ssl scheme", url: "ssl://127.0.0.1:8883"},
		{name: "missing port", url: "mqtt://127.0.0.1"},
		{name: "missing host", url: "mqtt://:1883"},
		{name: "user info", url: "mqtt://user@127.0.0.1:1883"},
		{name: "user info with password", url: "mqtt://user:secret@127.0.0.1:1883"},
		{name: "trailing slash", url: "mqtt://127.0.0.1:1883/"},
		{name: "path", url: "mqtt://127.0.0.1:1883/ecowitt"},
		{name: "query", url: "mqtt://127.0.0.1:1883?keepalive=60"},
		{name: "fragment", url: "mqtt://127.0.0.1:1883#fragment"},
		{name: "zero port", url: "mqtt://127.0.0.1:0"},
		{name: "port overflow", url: "mqtt://127.0.0.1:70000"},
		{name: "non numeric port", url: "mqtt://127.0.0.1:abc"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			config := testConfig(t)
			config.MQTTURL = testCase.url
			_, err := newAdapter(&recordingSession{}, config, nil, newFakeDialer())
			if testCase.valid {
				if err != nil {
					t.Fatalf("valid broker URL rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("broker URL %q was accepted", testCase.url)
			}
			if testCase.url != "" && strings.Contains(err.Error(), testCase.url) {
				t.Fatalf("validation error repeated the URL: %s", err)
			}
		})
	}
}

// TestHandleUnexpectedCommandRejectsUnavailable protects the defensive
// lifecycle handler: an impossible Command is rejected as unavailable without
// publishing MQTT traffic or an Observation.
func TestHandleUnexpectedCommandRejectsUnavailable(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	responder := &recordingResponder{}
	err := harness.adapter.HandleUnexpectedCommand(t.Context(), adapter.Command{
		EntityID: "ent-anything", OperationName: "set",
	}, responder)
	if err != nil {
		t.Fatalf("HandleUnexpectedCommand: %v", err)
	}
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	if len(responder.unavailable) != 1 {
		t.Fatalf("unavailable rejections = %d, want exactly one", len(responder.unavailable))
	}
	if len(responder.rejected) != 0 {
		t.Fatalf("plain rejections = %d, want none", len(responder.rejected))
	}
	if published := harness.session.published(); len(published) != 0 {
		t.Fatalf("command handling published %d Observations", len(published))
	}
	if dials := harness.dialer.configsSeen(); len(dials) != 0 {
		t.Fatalf("command handling dialled MQTT %d times", len(dials))
	}
	if health := harness.session.health(); len(health) != 0 {
		t.Fatalf("command handling reported health %#v", health)
	}
}

// TestDiagnosticsNeverExposeSecrets protects the diagnostics boundary: normal
// operation, an ignored report, a rejected report, and a station-silent
// transition emit fixed classifications and counts, never the topic,
// PASSKEY, payload, station identity, or weather values.
func TestDiagnosticsNeverExposeSecrets(t *testing.T) {
	t.Parallel()

	handler := &bufferHandler{}
	harness := newRuntimeHarness(t, slog.New(handler))
	harness.establish(t)

	payload := string(loadFixture(t, "gw2000-ws90-report.txt"))
	wrongPasskey := strings.Replace(
		payload, "PASSKEY="+sanitizedPasskeyHex, "PASSKEY=ffffffffffffffffffffffffffffffff", 1,
	)
	harness.deliver(t, fixtureTopic, []byte(wrongPasskey), false)
	harness.deliver(t, "ecowitt/000000000000", []byte(payload), false)
	harness.deliver(t, fixtureTopic, []byte(payload+"%zz"), false)
	harness.deliver(t, fixtureTopic, []byte(payload), true)
	harness.deliver(t, fixtureTopic, []byte(payload), false)
	harness.advance(t, 3*fixtureUploadInterval)
	harness.deliver(t, fixtureTopic, []byte(payload), false)

	output := handler.output()
	for _, forbidden := range []string{
		sanitizedPasskeyHex,
		"PASSKEY=",
		"943cc64457a7",
		"GW2000B_V3.3.2",
		"tempf=",
		"71.96",
		"29.046",
		"65.84",
		"ecowitt/",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("diagnostics exposed %q:\n%s", forbidden, output)
		}
	}
	for _, expected := range []string{
		wrongPasskeyErrorCode,
		unexpectedTopicIgnoredCode,
		malformedReportErrorCode,
		retainedReportIgnoredCode,
		duplicateReportIgnoredCode,
		stationSilentReason,
		"payload_bytes",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("diagnostics omitted the fixed classification %q:\n%s", expected, output)
		}
	}
}

// TestRunFailsBeforeMQTTWhenRegistrationIsRejected protects the fixed startup
// order and terminal static configuration errors: a rejected registration stops
// the Adapter before any MQTT traffic.
func TestRunFailsBeforeMQTTWhenRegistrationIsRejected(t *testing.T) {
	t.Parallel()

	registrationFailure := errors.New("sentinel registration rejection")
	session := &recordingSession{registerErr: registrationFailure, publishStarted: make(chan struct{})}
	dialer := newFakeDialer()
	ecowitt, err := newAdapter(session, testConfig(t), nil, dialer)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	runErr := ecowitt.Run(t.Context())
	if !errors.Is(runErr, registrationFailure) {
		t.Fatalf("Run error = %v, want the registration rejection", runErr)
	}
	if dials := dialer.configsSeen(); len(dials) != 0 {
		t.Fatalf("Run dialled MQTT %d times after a registration rejection", len(dials))
	}
}

// TestRunReturnsNilOnParentCancellation protects graceful shutdown.
func TestRunReturnsNilOnParentCancellation(t *testing.T) {
	t.Parallel()

	session := &recordingSession{publishStarted: make(chan struct{})}
	dialer := newFakeDialer()
	ecowitt, err := newAdapter(session, testConfig(t), nil, dialer)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	ecowitt.retryDelay = func(time.Duration) time.Duration { return 0 }
	ctx, cancel := context.WithCancel(t.Context())
	runResult := make(chan error, 1)
	go func() { runResult <- ecowitt.Run(ctx) }()
	eventually(t, "the first MQTT dial", func() bool { return len(dialer.configsSeen()) >= 1 })
	cancel()
	select {
	case runErr := <-runResult:
		if runErr != nil {
			t.Fatalf("Run error = %v, want graceful shutdown", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}
