package hearthd //nolint:testpackage // Test exercises package-private application assembly and lifecycle.

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestRunListsOwnedMappingsAndDrainsEndpoint(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})

	probeSubject, err := natswire.OwnedMappingsSubject(
		"inventory-probe", "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
	)
	if err != nil {
		t.Fatal(err)
	}
	runContext, stopCore := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		runErrors <- Run(runContext, Config{
			HTTPAddr:   unusedLoopbackAddress(t),
			NATSURL:    server.ClientURL(),
			SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
		}, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		stopCore()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("hearthd did not stop")
		}
	})

	waitForOwnedMappingsServer(t, server, probeSubject, runErrors)

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "inventory-owner", SoftwareName: "inventory-test",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "office-light",
		Device:     adapter.DeviceDescriptor{Name: "Office light", Kind: "light"},
		Entities: []adapter.EntityDescriptor{{
			Key: "power", ExternalID: "office-light.power", Name: "Power", Type: "hearth.power/v1",
			Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := session.ListOwnedMappings(ctx, adapter.OwnedMappingPageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor != "" {
		t.Fatalf("owned mappings page = %#v", page)
	}
	mapping := page.Items[0]
	if mapping.BindingKey != binding.BindingKey || mapping.DeviceID != binding.DeviceID ||
		mapping.EntityKey != binding.Entities[0].Key || mapping.EntityID != binding.Entities[0].EntityID {
		t.Fatalf("owned mapping = %#v, binding = %#v", mapping, binding)
	}

	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	stopCoreAndWait(t, stopCore, runErrors)

	waitForOwnedMappingsSubscriptionCount(t, server, probeSubject, 0)
}

func waitForOwnedMappingsServer(
	t *testing.T,
	server *natsserver.Server,
	probeSubject string,
	runErrors <-chan error,
) {
	t.Helper()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		subscriptions, err := server.Subsz(&natsserver.SubszOptions{
			Subscriptions: true,
			Test:          probeSubject,
		})
		if err != nil {
			return false, err
		}
		return subscriptions.Total == 1, nil
	})
}

func waitForOwnedMappingsSubscriptionCount(
	t *testing.T,
	server *natsserver.Server,
	probeSubject string,
	want int,
) {
	t.Helper()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		subscriptions, err := server.Subsz(&natsserver.SubszOptions{
			Subscriptions: true,
			Test:          probeSubject,
		})
		if err != nil {
			return false, err
		}
		return subscriptions.Total == want, nil
	})
}

func stopCoreAndWait(t *testing.T, stopCore context.CancelFunc, runErrors <-chan error) {
	t.Helper()
	stopCore()
	select {
	case err := <-runErrors:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hearthd did not stop")
	}
}
