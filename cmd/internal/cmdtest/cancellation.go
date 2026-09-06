package cmdtest

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

const executableShutdownTimeout = 10 * time.Second

// StartProcessNATS starts a loopback NATS server without claim responders,
// keeping adapter startup in the claim retry loop until it is canceled.
func StartProcessNATS(t *testing.T) string {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, NoSigs: true, NoLog: true,
		JetStream: true, StoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	if !server.ReadyForConnections(executableShutdownTimeout) {
		t.Fatal("embedded NATS server did not become ready")
	}
	return server.ClientURL()
}

// FreeLoopbackPort reserves and releases a loopback port for executable configs.
func FreeLoopbackPort(t *testing.T) string {
	t.Helper()
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// startupLogWriter captures stderr and signals when configuration has loaded.
// Its buffer is read only after command.Wait has joined the copying goroutine.
type startupLogWriter struct {
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
}

func (writer *startupLogWriter) Write(data []byte) (int, error) {
	n, err := writer.buffer.Write(data)
	if bytes.Contains(writer.buffer.Bytes(), []byte(`"event":"process.config_loaded"`)) {
		writer.once.Do(func() { close(writer.ready) })
	}
	return n, err
}

// CheckStartupCancellation interrupts a configured executable during startup
// and requires successful process.stopped output without process.failed.
func CheckStartupCancellation(t *testing.T, binary, configYAML, binaryName string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), executableShutdownTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary,
		"--config", configPath, "--log-level", "info", "--log-format", "json")
	writer := &startupLogWriter{ready: make(chan struct{})}
	command.Stderr = writer
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-finished
		}
	}()
	select {
	case <-writer.ready:
	case err := <-finished:
		joined = true
		t.Fatalf("%s exited before configuration loaded: %v\n%s", binaryName, err, writer.buffer.String())
	case <-ctx.Done():
		t.Fatal("executable did not load configuration before timeout")
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := <-finished
	joined = true
	if err != nil {
		t.Fatalf("%s failed after startup cancellation: %v\n%s", binaryName, err, writer.buffer.String())
	}
	records := DecodeLogRecords(t, writer.buffer.String())
	if failed := FindEvent(records, "process.failed"); failed != nil {
		t.Fatalf("%s reported failure on startup cancellation: %#v", binaryName, failed)
	}
	stopped := FindEvent(records, "process.stopped")
	if stopped == nil {
		t.Fatalf("%s lacks process.stopped:\n%s", binaryName, writer.buffer.String())
	}
	RequireField(t, stopped, "app", binaryName)
	RequireField(t, stopped, "component", "process")
}
