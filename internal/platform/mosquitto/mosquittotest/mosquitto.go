// Package mosquittotest starts disposable real broker containers for
// integration tests, alongside the dbtest and natstest helpers. Tests that
// need a real MQTT broker call StartMosquitto: Testcontainers isolates each
// broker on a random loopback host port and removes it afterward.
package mosquittotest

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	testmosquitto "github.com/testcontainers/testcontainers-go/modules/mosquitto"
)

// MosquittoImage pins the disposable MQTT test broker image so local and CI
// integration tests exercise the same version as broker-images-pull.
const MosquittoImage = "eclipse-mosquitto:2.0.22"

// MosquittoTestConfig is the checked-in Mosquitto config copied into each test
// container. It keeps persistence disabled so containers stay disposable.
const MosquittoTestConfig = "mosquitto.test.conf"

const (
	mosquittoContainerPort = "1883/tcp"
	containerStopTimeout   = 30 * time.Second
)

// Mosquitto is one disposable Mosquitto container owned by a single test.
type Mosquitto struct {
	container *testmosquitto.Container
	url       string
	stop      sync.Once
}

// StartMosquitto starts a disposable Mosquitto container on a Docker-assigned
// loopback host port and registers test cleanup. It always requires a working
// container runtime: without one the test fails instead of skipping, so
// real-broker coverage can never go silently unexercised.
func StartMosquitto(t *testing.T) *Mosquitto {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), containerStopTimeout)
	defer cancel()
	mosquittoContainer, err := testmosquitto.Run(
		ctx,
		MosquittoImage,
		testmosquitto.WithConfigFile(repositoryConfigPath(t, MosquittoTestConfig)),
		loopbackMosquittoPort(),
	)
	broker := &Mosquitto{container: mosquittoContainer}
	t.Cleanup(broker.Stop)
	if err != nil {
		t.Fatalf("start real Mosquitto test broker: %v", err)
	}
	broker.url, err = mosquittoContainer.BrokerURL(ctx)
	if err != nil {
		broker.Stop()
		t.Fatalf("resolve real Mosquitto test broker URL: %v", err)
	}
	return broker
}

// URL returns the broker's Paho-compatible mqtt:// connection URL.
func (broker *Mosquitto) URL() string {
	return broker.url
}

// Stop removes the broker container. It is idempotent and bounded so a test
// that ends the broker early and test cleanup can both call it.
func (broker *Mosquitto) Stop() {
	broker.stop.Do(func() {
		if broker.container == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), containerStopTimeout)
		defer cancel()
		_ = broker.container.Terminate(ctx)
	})
}

func loopbackMosquittoPort() testcontainers.ContainerCustomizer {
	return testcontainers.WithHostConfigModifier(func(hostConfig *container.HostConfig) {
		hostConfig.PortBindings = network.PortMap{
			network.MustParsePort(mosquittoContainerPort): {
				{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"},
			},
		}
	})
}

// repositoryConfigPath resolves a checked-in config file from the repository
// root so the broker works regardless of a test package's working directory.
func repositoryConfigPath(t *testing.T, name string) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve mosquittotest source path")
	}
	// This file lives at internal/platform/mosquitto/mosquittotest, five
	// levels below the repository root.
	repositoryRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(sourceFile)))))
	path := filepath.Join(repositoryRoot, "configs", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("broker config %s: %v", path, err)
	}
	return path
}
