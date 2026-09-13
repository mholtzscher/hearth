// Package testbroker starts disposable real broker containers for integration
// tests. Tests that need a real MQTT broker call StartMosquitto: the container
// is isolated per test with a random loopback host port, the pinned
// eclipse-mosquitto image, and t.Cleanup removal.
package testbroker

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// MosquittoImage pins the MQTT broker image so local and CI runs exercise the
// same broker version. It must match the image in compose.yaml.
const MosquittoImage = "eclipse-mosquitto:2.0.22"

// RequireMosquittoEnv gates real-Mosquitto integration tests. When it is set to
// "1", an unavailable Docker daemon fails the test instead of skipping it. The
// mise test task sets it so `mise run test` always exercises a real broker.
const RequireMosquittoEnv = "HEARTH_REQUIRE_MOSQUITTO"

// MosquittoTestConfig is the checked-in Mosquitto config mounted into each test
// container. It keeps persistence disabled so containers stay disposable.
const MosquittoTestConfig = "mosquitto.test.conf"

// Bounded container lifecycle and probe timing.
const (
	dockerCommandTimeout     = 30 * time.Second
	mosquittoReadyTimeout    = 30 * time.Second
	mosquittoReadinessPoll   = 100 * time.Millisecond
	mqttReadinessDialTimeout = time.Second
	mqttReadinessReadTimeout = 2 * time.Second
)

// MQTT 3.1.1 wire constants for the readiness CONNECT and CONNACK probe.
const (
	mqttConnectPacketType       = 0x10
	mqttConnackPacketType       = 0x20
	mqttProtocolLevel311        = 0x04
	mqttCleanSessionFlags       = 0x02
	mqttKeepAliveSeconds        = 0x3c
	mqttLengthPrefixShift       = 8
	mqttConnackReadLength       = 4
	mqttConnackRemainingLength  = 2
	mqttConnackSuccessCode      = 0x00
	mqttRemainingLengthBase     = 128
	mqttRemainingLengthContinue = 0x80
)

// mqttProtocolName is the protocol-name field of an MQTT 3.1.1 CONNECT packet,
// and readinessClientID is a fixed anonymous client used only by the probe.
const (
	mqttProtocolName   = "MQTT"
	readinessClientID  = "hearth-testbroker-readiness"
	mqttProtocolLength = 0x04
	mqttKeepAliveHigh  = 0x00
)

// Mosquitto is one disposable Mosquitto container owned by a single test.
type Mosquitto struct {
	name string
	url  string
	stop sync.Once
}

// StartMosquitto starts a disposable Mosquitto container on a random loopback
// host port, waits until it answers an MQTT CONNECT, and registers t.Cleanup to
// remove the container. It skips the test when Docker is unavailable unless
// RequireMosquittoEnv is set, in which case it fails.
func StartMosquitto(t *testing.T) *Mosquitto {
	t.Helper()
	requireDocker(t)
	return startMosquitto(t)
}

// URL returns the broker's Paho-compatible tcp:// connection URL.
func (broker *Mosquitto) URL() string {
	return broker.url
}

// Stop removes the broker container. It is idempotent and bounded so a test
// that ends the broker early and t.Cleanup can both call it.
func (broker *Mosquitto) Stop() {
	broker.stop.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerCommandTimeout)
		defer cancel()
		// --rm already removed a stopped container, so a missing container is
		// expected and cleanup never fails a test.
		//nolint:gosec // The container name is generated from the process ID and port, never user input.
		_ = exec.CommandContext(ctx, "docker", "rm", "--force", broker.name).Run()
	})
}

func startMosquitto(t *testing.T) *Mosquitto {
	t.Helper()
	port := freeLoopbackPort(t)
	name := fmt.Sprintf("hearth-mosquitto-%d-%d", os.Getpid(), port)
	configPath := repositoryConfigPath(t, MosquittoTestConfig)
	broker := &Mosquitto{name: name, url: fmt.Sprintf("tcp://127.0.0.1:%d", port)}
	// Register cleanup before docker run so a detached container is still
	// removed if the Docker CLI times out after creating it.
	t.Cleanup(broker.Stop)
	runDocker(t,
		"run", "--detach", "--rm",
		"--name", name,
		"--publish", fmt.Sprintf("127.0.0.1:%d:1883", port),
		"--mount", "type=bind,source="+configPath+",target=/mosquitto/config/mosquitto.conf,readonly",
		MosquittoImage,
	)
	if !waitForMQTTReady(t.Context(), hostPort(broker.url), mosquittoReadyTimeout) {
		broker.Stop()
		t.Fatalf(
			"Mosquitto container %s did not answer an MQTT CONNECT on %s:\n%s",
			name, broker.url, containerLogs(name),
		)
	}
	return broker
}

// requireDocker skips the test when no Docker daemon is reachable, unless the
// required-coverage gate is set. Direct `go test` may skip; `mise run test`
// must fail because it sets RequireMosquittoEnv.
func requireDocker(t *testing.T) {
	t.Helper()
	if dockerDaemonAvailable() {
		return
	}
	if os.Getenv(RequireMosquittoEnv) == "1" {
		t.Fatalf("Docker daemon is required for real-Mosquitto tests because %s=1", RequireMosquittoEnv)
	}
	t.Skipf("skipping real-Mosquitto test: Docker daemon is unavailable (set %s=1 to require it)", RequireMosquittoEnv)
}

func dockerDaemonAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockerCommandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run() == nil
}

func containerLogs(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), dockerCommandTimeout)
	defer cancel()
	output, _ := exec.CommandContext(ctx, "docker", "logs", name).CombinedOutput()
	return string(output)
}

func runDocker(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dockerCommandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

// waitForMQTTReady polls until the broker answers an MQTT 3.1.1 CONNECT with a
// success CONNACK. A plain TCP probe is insufficient: Docker's loopback
// userland proxy accepts host connections before Mosquitto starts listening, so
// a TCP dial can succeed and the following MQTT connect still be reset.
func waitForMQTTReady(ctx context.Context, address string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if mqttConnackSucceeds(ctx, address) {
			return true
		}
		time.Sleep(mosquittoReadinessPoll)
	}
	return false
}

// mqttConnackSucceeds sends a minimal MQTT 3.1.1 clean-session CONNECT with an
// anonymous client and reports whether the broker answers CONNACK code 0.
func mqttConnackSucceeds(ctx context.Context, address string) bool {
	dialer := net.Dialer{Timeout: mqttReadinessDialTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	defer func() { _ = connection.Close() }()
	if err = connection.SetDeadline(time.Now().Add(mqttReadinessReadTimeout)); err != nil {
		return false
	}
	payload := []byte{0x00, mqttProtocolLength}
	payload = append(payload, mqttProtocolName...)
	payload = append(payload,
		mqttProtocolLevel311, mqttCleanSessionFlags, mqttKeepAliveHigh, mqttKeepAliveSeconds,
	)
	payload = append(payload,
		byte(len(readinessClientID)>>mqttLengthPrefixShift), byte(len(readinessClientID)),
	)
	payload = append(payload, readinessClientID...)
	packet := append([]byte{mqttConnectPacketType}, encodeRemainingLength(len(payload))...)
	packet = append(packet, payload...)
	if _, err = connection.Write(packet); err != nil {
		return false
	}
	response := make([]byte, mqttConnackReadLength)
	if _, err = io.ReadFull(connection, response); err != nil {
		return false
	}
	return response[0] == mqttConnackPacketType &&
		response[1] == mqttConnackRemainingLength &&
		response[3] == mqttConnackSuccessCode
}

// encodeRemainingLength encodes an MQTT remaining-length field.
func encodeRemainingLength(length int) []byte {
	var encoded []byte
	for {
		digit := length % mqttRemainingLengthBase
		length /= mqttRemainingLengthBase
		if length > 0 {
			digit |= mqttRemainingLengthContinue
		}
		//nolint:gosec // digit is bounded to 0..255 by the remaining-length encoding above.
		encoded = append(encoded, byte(digit))
		if length == 0 {
			return encoded
		}
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		t.Fatalf("loopback listener address %T is not TCP", listener.Addr())
	}
	port := address.Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func hostPort(url string) string {
	return strings.TrimPrefix(url, "tcp://")
}

// repositoryConfigPath resolves a checked-in config file from the repository
// root so the harness works regardless of a test package's working directory.
func repositoryConfigPath(t *testing.T, name string) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve testbroker source path")
	}
	repositoryRoot := filepath.Dir(filepath.Dir(filepath.Dir(sourceFile)))
	path := filepath.Join(repositoryRoot, "configs", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("broker config %s: %v", path, err)
	}
	return path
}
