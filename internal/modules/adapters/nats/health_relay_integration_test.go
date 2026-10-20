//go:build integration

package nats //nolint:testpackage // Exercises relay with actual JetStream publication.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestHealthRelayRediscoveryAndImmutableRetry(t *testing.T) {
	t.Parallel()
	js, conn := startHealthJetStream(t)
	if err := ProvisionHealthStream(context.Background(), js); err != nil {
		t.Fatal(err)
	}
	subscription, err := conn.SubscribeSync("hearth.v1.core.adapter-health.*")
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := conn.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	store := newHealthTestStore()
	store.deleteFailures = 1
	relay := startTestHealthRelay(t, js, store)
	select {
	case <-store.empty:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not read empty outbox")
	}
	row := healthTestRow()
	store.enqueue(row) // Deliberately lose the post-commit wake hint.
	first, err := subscription.NextMsg(7 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := subscription.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Data) != string(retry.Data) || first.Subject != retry.Subject ||
		first.Header.Get("Nats-Msg-Id") != string(row.ID) ||
		first.Header.Get("traceparent") != row.TraceParent ||
		first.Header.Get("tracestate") != row.TraceState ||
		retry.Header.Get("Nats-Msg-Id") != string(row.ID) {
		t.Fatalf("retry changed publication: %+v %+v", first, retry)
	}
	select {
	case id := <-store.deletions:
		if id != row.ID {
			t.Fatalf("deleted %s", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("acknowledged row was not deleted")
	}
	stream, err := js.Stream(context.Background(), HealthStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("retry identity did not deduplicate: %+v", info.State)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if drainErr := relay.Drain(ctx); drainErr != nil {
		t.Fatal(drainErr)
	}
	select {
	case <-relay.Closed():
	default:
		t.Fatal("Drain did not join publisher")
	}
	if relay.Active() || relay.Fault() != nil {
		t.Fatalf("stopped healthy relay: active=%v fault=%v", relay.Active(), relay.Fault())
	}
}

func TestHealthRelayPreservesPoison(t *testing.T) {
	t.Parallel()
	js, _ := startHealthJetStream(t)
	store := newHealthTestStore()
	row := healthTestRow()
	row.Snapshot.Revision = 0
	store.enqueue(row)
	relay := startTestHealthRelay(t, js, store)
	select {
	case <-relay.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("poison did not stop relay")
	}
	if relay.Active() || relay.Fault() == nil {
		t.Fatal("poison did not fail readiness")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.rows) != 1 {
		t.Fatal("poison was deleted")
	}
}

func TestHealthRelayRejectsPubAckFromWrongStream(t *testing.T) {
	t.Parallel()
	js, conn := startHealthJetStream(t)
	if _, err := js.CreateStream(
		context.Background(),
		jetstream.StreamConfig{Name: "WRONG", Subjects: []string{"hearth.v1.core.adapter-health.*"}},
	); err != nil {
		t.Fatal(err)
	}
	subscription, err := conn.SubscribeSync("hearth.v1.core.adapter-health.*")
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := conn.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	store := newHealthTestStore()
	store.enqueue(healthTestRow())
	relay := startTestHealthRelay(t, js, store)
	// A second real publication proves the first PubAck did not delete the row.
	for range 2 {
		if _, receiveErr := subscription.NextMsg(5 * time.Second); receiveErr != nil {
			t.Fatal(receiveErr)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = relay.Stop(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.rows) != 1 || relay.Fault() == nil {
		t.Fatal("wrong-stream acknowledgement erased pending work")
	}
}

func TestHealthRelayRetainsWorkDuringCapacityRejectionAndRecoversReadiness(t *testing.T) {
	t.Parallel()
	js, conn := startHealthJetStream(t)
	config := healthStreamConfig()
	config.MaxMsgs = 1 // Small capacity, unchanged WorkQueue/DiscardNew behavior.
	stream, err := js.CreateStream(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, publishErr := js.Publish(
		context.Background(),
		"hearth.v1.core.adapter-health.occupied",
		[]byte("retained"),
	); publishErr != nil {
		t.Fatal(publishErr)
	}
	subscription, err := conn.SubscribeSync("hearth.v1.core.adapter-health.zigbee-main")
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := conn.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	store := newHealthTestStore()
	row := healthTestRow()
	store.enqueue(row)
	relay := startTestHealthRelay(t, js, store)
	for range 2 {
		if _, receiveErr := subscription.NextMsg(5 * time.Second); receiveErr != nil {
			t.Fatal(receiveErr)
		}
	}
	store.mu.Lock()
	pending := len(store.rows)
	store.mu.Unlock()
	if pending != 1 || relay.Fault() == nil {
		t.Fatal("capacity rejection lost work or remained ready")
	}
	if deleteErr := stream.DeleteMsg(context.Background(), 1); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	select {
	case id := <-store.deletions:
		if id != row.ID {
			t.Fatalf("deleted %s", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capacity recovery did not drain retained work")
	}
	select {
	case <-store.empty:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not rediscover empty outbox")
	}
	if relay.Fault() != nil {
		t.Fatal("recovered dependency left a remembered readiness fault")
	}
}

func TestHealthRelayDrainTimeoutDoesNotStartAnotherPublisherOrClaimJoin(t *testing.T) {
	t.Parallel()
	js, conn := startHealthJetStream(t)
	if err := ProvisionHealthStream(context.Background(), js); err != nil {
		t.Fatal(err)
	}
	subscription, err := conn.SubscribeSync("hearth.v1.core.adapter-health.*")
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := conn.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	store := newHealthTestStore()
	store.deletePermit = make(chan struct{})
	store.deleteEntered = make(chan struct{}, 4)
	store.enqueue(healthTestRow())
	relay := startTestHealthRelay(t, js, store)
	select {
	case <-store.deleteEntered:
	case <-time.After(5 * time.Second):
		close(store.deletePermit)
		t.Fatal("publication did not reach acknowledged delete")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	drainErr := relay.Drain(ctx)
	if !errors.Is(drainErr, context.Canceled) {
		close(store.deletePermit)
		t.Fatalf("drain timeout = %v", drainErr)
	}
	select {
	case <-relay.Closed():
		close(store.deletePermit)
		t.Fatal("cancel was reported as joined")
	default:
	}
	close(store.deletePermit)
	select {
	case <-relay.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("released publisher did not exit")
	}
	if flushErr := conn.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	pending, _, pendingErr := subscription.Pending()
	if pendingErr != nil {
		t.Fatal(pendingErr)
	}
	if pending != 1 || len(store.deleteEntered) != 0 {
		t.Fatalf("competing drain publisher: publications=%d extra deletes=%d", pending, len(store.deleteEntered))
	}
}
