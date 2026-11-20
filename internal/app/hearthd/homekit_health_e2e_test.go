//go:build e2e

package hearthd //nolint:testpackage // Exercises the bridge's startup health through production Core.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	homekitbridge "github.com/mholtzscher/hearth/internal/adapters/homekit"
	homekitapp "github.com/mholtzscher/hearth/internal/app/homekit"
	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/httpapi"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const homeKitHealthAdapterID = "homekit-running-health"

func TestHomeKitStartupReportsFaultsWithoutReportingHealthy(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	natsURL := broker.ClientURL()
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC", NATSURL: natsURL,
		SQLitePath: dbtest.MigratedPath(t), Agent: requiredAgentConfig(t),
	})
	client := newNonPoolingHTTPClient(t)
	logger := slog.New(slog.DiscardHandler)
	heldPort, err := net.ListenTCP("tcp", &net.TCPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer heldPort.Close()
	port := heldPort.Addr().(*net.TCPAddr).Port
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()

	for _, tc := range []struct {
		id, coreURL, reason string
	}{
		{"homekit-health-core", unavailable.URL, "core_unreachable"},
		{"homekit-health-hap", "http://" + core.address, "hap_server_failed"},
	} {
		config := homekitapp.Config{
			AdapterID: tc.id, NATSURL: &natsURL, CoreURL: tc.coreURL, ListenPort: &port,
			DataDir: filepath.Join(t.TempDir(), "bridge"), Interfaces: []string{"lo"},
		}
		reason := "adapter.hearth-adapter-homekit." + tc.reason
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		runErr := homekitapp.Run(ctx, config, logger)
		cancel()
		if runErr == nil {
			t.Fatalf("%s started despite its blocked dependency", tc.id)
		}
		assertHomeKitStartupHealth(t, client, core, tc.id, reason)
	}
}

func homeKitHealthJetStream(t *testing.T, natsURL string) jetstream.JetStream {
	t.Helper()
	conn, err := natsgo.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func assertHomeKitStartupHealth(t *testing.T, client *http.Client, core *functionCoreRun, id, reason string) {
	t.Helper()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		status, body, err := functionNetworkRequest(t.Context(), client, core.address, http.MethodGet,
			"/v1/adapters/"+id+"/health/history", "")
		if err != nil || status == http.StatusNotFound {
			return false, err
		}
		if status != http.StatusOK {
			t.Fatalf("HomeKit health history returned HTTP %d", status)
		}
		var history adaptersapi.AdapterHealthTransitionCollectionBody
		homekitJourneyDecode(t, body, &history)
		found := false
		for _, entry := range history.Items {
			if entry.Status == "healthy" {
				t.Fatalf("HomeKit reported healthy before %s cleared", reason)
			}
			if entry.Status == "unhealthy" && entry.Reason != nil && entry.Reason.Code == reason {
				found = true
			}
		}
		return found, nil
	})
}

func TestHomeKitRunningHealthTracksFactLossAndStaleInventory(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	natsURL := broker.ClientURL()
	core := startFunctionCore(t, Config{HouseholdTimezone: "UTC", NATSURL: natsURL,
		SQLitePath: dbtest.MigratedPath(t), Agent: requiredAgentConfig(t)})
	target := &url.URL{Scheme: "http", Host: core.address}
	relay := httputil.NewSingleHostReverseProxy(target)
	var rejectInventory atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if rejectInventory.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		relay.ServeHTTP(w, req)
	}))
	t.Cleanup(proxy.Close)
	const factsUnavailable = "adapter.hearth-adapter-homekit.facts_unavailable"
	// Isolate the bridge's Fact transport failure from Core's own Fact consumers.
	facts := homeKitHealthJetStream(t, startLifecycleNATSServer(t).ClientURL())
	startHomeKitHealthRuntime(t, natsURL, proxy.URL, facts)
	client := newNonPoolingHTTPClient(t)
	assertHomeKitStartupHealth(t, client, core, homeKitHealthAdapterID, factsUnavailable)
	if err := devicesnats.ProvisionDeviceFactStream(t.Context(), facts); err != nil {
		t.Fatal(err)
	}
	assertHomeKitHealth(t, client, core, "")
	waitForHomeKitFactPull(t, facts)
	if err := facts.DeleteStream(t.Context(), devicesnats.DeviceFactStreamName); err != nil {
		t.Fatal(err)
	}
	assertHomeKitHealth(t, client, core, factsUnavailable)
	rejectInventory.Store(true)
	assertHomeKitHealth(t, client, core, "adapter.hearth-adapter-homekit.core_unreachable")
	rejectInventory.Store(false)
	assertHomeKitHealth(t, client, core, factsUnavailable)
	if err := devicesnats.ProvisionDeviceFactStream(t.Context(), facts); err != nil {
		t.Fatal(err)
	}
	assertHomeKitHealth(t, client, core, "")
}

func waitForHomeKitFactPull(t *testing.T, facts jetstream.JetStream) {
	t.Helper()
	stream, err := facts.Stream(t.Context(), devicesnats.DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		list := stream.ListConsumers(t.Context())
		waiting := false
		for consumer := range list.Info() {
			waiting = waiting || consumer.NumWaiting > 0
		}
		return waiting, list.Err()
	})
}

func startHomeKitHealthRuntime(t *testing.T, natsURL, coreURL string, facts jetstream.JetStream) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	session, err := adapter.Connect(t.Context(), adapter.Config{
		AdapterID: homeKitHealthAdapterID, NATSURL: natsURL, SoftwareName: "homekit-health-fixture",
		SoftwareVersion: "1", Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	client, err := homekitbridge.NewCoreClient(coreURL)
	if err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(freeSimulatorControlAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	// Use the runtime directly so the test can exercise health deadlines without
	// waiting for the CLI's minimum 15-second resync interval.
	bridge, err := homekitbridge.NewRuntime(homekitbridge.RuntimeConfig{
		DataDir: filepath.Join(t.TempDir(), "bridge"), BridgeName: "Hearth Health " + portText,
		ListenPort: port, Interfaces: []string{"lo"}, ResyncInterval: time.Second,
	}, session, client, facts, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- bridge.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case runErr := <-done:
			if !errors.Is(runErr, context.Canceled) {
				t.Errorf("HomeKit runtime: %v", runErr)
			}
		case <-time.After(10 * time.Second):
			t.Error("HomeKit runtime did not stop")
		}
	})
}

func assertHomeKitHealth(t *testing.T, client *http.Client, core *functionCoreRun, reason string) {
	t.Helper()
	waitForMatrixCondition(t, 8*time.Second, func() (bool, error) {
		status, raw, err := functionNetworkRequest(
			t.Context(),
			client,
			core.address,
			http.MethodGet,
			"/v1/adapters/"+homeKitHealthAdapterID,
			"",
		)
		if err != nil || status == http.StatusNotFound {
			return false, err
		}
		if status != http.StatusOK {
			t.Fatalf("HomeKit health returned HTTP %d", status)
		}
		var body adaptersapi.AdapterBody
		homekitJourneyDecode(t, raw, &body)
		if reason == "" {
			return body.Health.Status == "healthy" && body.Health.Reason == nil, nil
		}
		return body.Health.Status == "unhealthy" && body.Health.Reason != nil && body.Health.Reason.Code == reason, nil
	})
}
