package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestCoreEnqueuesStartupInterruptionFactsBeforeJetStreamProvisioning protects
// the §8.4/§12 startup ordering: interruption facts are enqueued as soon as
// both connections, the epoch fence and the dispatcher are live, and before
// JetStream provisioning can fail. A broker without JetStream fails startup at
// provision_jetstream after that enqueue, so the dispatcher's error-path drain
// still publishes one interrupted fact per retained Command.
//
// The observed facts are the oracle: provisioning ahead of the enqueue would
// publish nothing here, and no later startup's interruption call returns the
// committed rows again, so those facts would be lost permanently.
func TestCoreEnqueuesStartupInterruptionFactsBeforeJetStreamProvisioning(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	entityID, requestedCorrelations := seedRetainedActiveCommands(ctx, t, databasePath)

	// JetStream stays disabled, so the provisioning stage fails after both
	// connections, the epoch fence and the bounded dispatcher are already live.
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})

	subscriber := newDeviceFactSubscriber(t, server.ClientURL())
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{
			HouseholdTimezone: "UTC", HTTPAddr: unusedLoopbackAddress(t),
			NATSURL: server.ClientURL(), SQLitePath: databasePath,
		}, slog.New(slog.DiscardHandler))
	}()

	seen := map[string]bool{}
	for range len(requestedCorrelations) {
		record := subscriber.next(t)
		if record.route.Family != natswire.DeviceFactFamilyCommand ||
			record.route.Variant != string(devices.CommandStatusInterrupted) {
			t.Fatalf("startup interruption fact route = %#v", record.route)
		}
		if record.route.EntityID != string(entityID) {
			t.Fatalf("interruption fact Entity = %q, want %q", record.route.EntityID, entityID)
		}
		if !requestedCorrelations[record.envelope.CorrelationID] {
			t.Fatalf(
				"interruption correlation = %q, want one of the retained Commands",
				record.envelope.CorrelationID,
			)
		}
		if seen[record.envelope.CausationID] {
			t.Fatalf("Command %q published more than one interruption fact", record.envelope.CausationID)
		}
		seen[record.envelope.CausationID] = true
	}
	if len(seen) != len(requestedCorrelations) {
		t.Fatalf("interruption facts = %d, want %d", len(seen), len(requestedCorrelations))
	}
	subscriber.assertNone(t)

	select {
	case runErr := <-runErrors:
		if stage := ErrorStage(runErr); stage != "provision_jetstream" {
			t.Fatalf("startup failed at stage %q (%v), want provision_jetstream", stage, runErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("hearthd startup did not fail on a broker without JetStream")
	}
}
