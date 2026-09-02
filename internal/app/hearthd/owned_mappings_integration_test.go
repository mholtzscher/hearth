package hearthd //nolint:testpackage // Test exercises package-private application assembly and lifecycle.

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

//nolint:gocognit,cyclop // One end-to-end test keeps assembly, persistence, pagination, and shutdown in one lifecycle.
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
	runConfig := Config{
		HTTPAddr:   unusedLoopbackAddress(t),
		NATSURL:    server.ClientURL(),
		SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
	}
	runErrors := make(chan error, 1)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		runErrors <- Run(runContext, runConfig, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		stopCore()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Error("hearthd did not stop")
		}
	})

	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		subscriptions, subscriptionErr := server.Subsz(&natsserver.SubszOptions{
			Subscriptions: true,
			Test:          probeSubject,
		})
		if subscriptionErr != nil {
			return false, subscriptionErr
		}
		return subscriptions.Total == 1, nil
	})

	owner, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "inventory-owner", SoftwareName: "inventory-test",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	other, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "inventory-other", SoftwareName: "inventory-test",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })

	empty, err := owner.ListOwnedMappings(ctx, adapter.OwnedMappingPageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Items) != 0 || empty.NextCursor != "" {
		t.Fatalf("mappings before registration = %#v", empty)
	}

	alpha, err := owner.Register(ctx, ownedMappingsRegistration("alpha-light", "power", "brightness"))
	if err != nil {
		t.Fatal(err)
	}
	zeta, err := owner.Register(ctx, ownedMappingsRegistration("zeta-light", "power"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Register(ctx, ownedMappingsRegistration("middle-light", "power")); err != nil {
		t.Fatal(err)
	}

	first, err := owner.ListOwnedMappings(ctx, adapter.OwnedMappingPageRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := []adapter.OwnedMapping{
		ownedMappingFromBinding(t, alpha, "brightness"),
		ownedMappingFromBinding(t, alpha, "power"),
	}
	if !reflect.DeepEqual(first.Items, wantFirst) || first.NextCursor == "" {
		t.Fatalf("first mappings page = %#v, want items %#v and a cursor", first, wantFirst)
	}

	second, err := owner.ListOwnedMappings(ctx, adapter.OwnedMappingPageRequest{
		Limit: 2, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSecond := []adapter.OwnedMapping{ownedMappingFromBinding(t, zeta, "power")}
	if !reflect.DeepEqual(second.Items, wantSecond) || second.NextCursor != "" {
		t.Fatalf("second mappings page = %#v, want terminal items %#v", second, wantSecond)
	}

	if closeErr := other.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if closeErr := owner.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hearthd did not stop")
	}

	subscriptions, err := server.Subsz(&natsserver.SubszOptions{
		Subscriptions: true,
		Test:          probeSubject,
	})
	if err != nil {
		t.Fatal(err)
	}
	if subscriptions.Total != 0 {
		t.Fatalf("owned mappings subscriptions after shutdown = %d, want 0", subscriptions.Total)
	}
}

func ownedMappingsRegistration(bindingKey string, entityKeys ...string) adapter.Registration {
	entities := make([]adapter.EntityDescriptor, len(entityKeys))
	for index, key := range entityKeys {
		entities[index] = adapter.EntityDescriptor{
			Key: key, ExternalID: bindingKey + "." + key, Name: key, Type: "hearth.power/v1",
			Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		}
	}
	return adapter.Registration{
		BindingKey: bindingKey,
		Device:     adapter.DeviceDescriptor{Name: bindingKey, Kind: "light"},
		Entities:   entities,
	}
}

func ownedMappingFromBinding(t *testing.T, binding adapter.Binding, entityKey string) adapter.OwnedMapping {
	t.Helper()
	for _, entity := range binding.Entities {
		if entity.Key == entityKey {
			return adapter.OwnedMapping{
				BindingKey: binding.BindingKey,
				DeviceID:   binding.DeviceID,
				EntityKey:  entity.Key,
				EntityID:   entity.EntityID,
			}
		}
	}
	t.Fatalf("binding %q omitted entity %q", binding.BindingKey, entityKey)
	return adapter.OwnedMapping{}
}
