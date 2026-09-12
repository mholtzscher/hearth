package nats //nolint:testpackage // Tests exercise package-private stream and consumer policy.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestDeviceFactStreamDurableConsumerResumesFromAcknowledgementFloor proves
// consumer policy is consumer-owned: Core provisioning creates only the stream,
// and a reader that creates a named durable consumer, acknowledges part of the
// stream and returns resumes from its own ack floor instead of replaying the
// facts it already processed.
func TestDeviceFactStreamDurableConsumerResumesFromAcknowledgementFloor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, _, js := startJetStream(t)
	if err := ProvisionDeviceFactStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	published := publishTestDeviceFacts(t, js, testTailFacts(t, 3)...)

	stream, err := js.Stream(ctx, DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	const consumerName = "hearthd-test-resume-v1"
	consumer := createTestDeviceFactConsumer(t, stream, consumerName, jetstream.DeliverAllPolicy)
	firstRun := fetchTestDeviceFactIDs(t, consumer, 2)
	if !slices.Equal(firstRun, published[:2]) {
		t.Fatalf("first run delivered %v, want %v", firstRun, published[:2])
	}
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 2 && info.NumAckPending == 0
	})

	resumed := createTestDeviceFactConsumer(t, stream, consumerName, jetstream.DeliverAllPolicy)
	if floor := resumed.CachedInfo().AckFloor.Stream; floor != 2 {
		t.Fatalf("resumed consumer ack floor = %d, want 2", floor)
	}
	resumedRun := fetchTestDeviceFactIDs(t, resumed, 1)
	if len(resumedRun) != 1 || resumedRun[0] != published[2] {
		t.Fatalf("resumed run delivered %v, want only the unacknowledged %v", resumedRun, published[2])
	}
}

// TestDeviceFactStreamDeliverNewConsumerSkipsExistingFacts proves a new reader
// that wants only future facts gets none of the retained stream and then sees
// every fact published after it asked, so a tailing consumer chooses its own
// delivery policy without Core provisioning anything for it.
func TestDeviceFactStreamDeliverNewConsumerSkipsExistingFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, _, js := startJetStream(t)
	if err := ProvisionDeviceFactStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	publishTestDeviceFacts(t, js, testTailFacts(t, 2)...)

	stream, err := js.Stream(ctx, DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	consumer := createTestDeviceFactConsumer(t, stream, "hearthd-test-tail-v1", jetstream.DeliverNewPolicy)
	batch, err := consumer.FetchNoWait(4)
	if err != nil {
		t.Fatal(err)
	}
	delivered := 0
	for range batch.Messages() {
		delivered++
	}
	if delivered != 0 {
		t.Fatalf("a DeliverNew consumer received %d existing facts", delivered)
	}

	tail := pendingFact(3, testObservationFact(
		t, mustEntityID(t), time.Now().UTC().Add(2*time.Minute), `"tail"`, devices.DispositionApplied,
	))
	published := publishTestDeviceFacts(t, js, tail)
	ids := fetchTestDeviceFactIDs(t, consumer, 1)
	if len(ids) != 1 || ids[0] != published[0] {
		t.Fatalf("tail consumer delivered %v, want only %v", ids, published[0])
	}
}

// TestDeviceFactStreamProvisioningCreatesNoConsumer proves Core owns only the
// stream: provisioning twice leaves zero consumers, so no ack floor, filter or
// delivery policy is pinned for a reader Core does not have.
func TestDeviceFactStreamProvisioningCreatesNoConsumer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, _, js := startJetStream(t)
	if err := ProvisionDeviceFactStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionDeviceFactStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Consumers != 0 {
		t.Fatalf("Core provisioned %d consumers, want none", info.State.Consumers)
	}
	if _, consumerErr := stream.Consumer(ctx, "hearthd-device-facts-v1"); !errors.Is(
		consumerErr, jetstream.ErrConsumerNotFound,
	) {
		t.Fatalf("stream reports a Core-owned device fact consumer: %v", consumerErr)
	}
}

// TestDeviceFactRelayDeduplicatesOnTheStableMessageID proves a retry is
// deduplicated inside the broker's bounded duplicate window: a delete failure
// republishes the row under the same Nats-Msg-Id, the broker acknowledges it as a
// duplicate, and the stream still holds exactly one fact for the durable row. The
// window is bounded, so a reader must stay idempotent on the stable identity for
// a retry that arrives after it.
func TestDeviceFactRelayDeduplicatesOnTheStableMessageID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, _, js := startJetStream(t)
	if err := ProvisionDeviceFactStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	outbox.failDeletes(1)
	outbox.enqueue(pendingFact(1, testObservationFact(
		t, mustEntityID(t), time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), `true`, devices.DispositionApplied,
	)))
	relay, err := startDeviceFactRelay(
		outbox, testDeviceFactValidator(t), discardLogger(), jsDeviceFactPublisher(js), testDeviceFactRelayOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), testFactLiveness)
		defer cancelDrain()
		_ = relay.Drain(drainContext)
	})
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 1 })

	stream, err := js.Stream(ctx, DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 || info.State.LastSeq != 1 {
		t.Fatalf("stream holds %d messages with last sequence %d, want the retry deduplicated",
			info.State.Msgs, info.State.LastSeq)
	}
}

// publishTestDeviceFacts publishes the supplied pending facts through a real
// relay into the embedded stream and returns their identities in enqueue order.
func publishTestDeviceFacts(
	t *testing.T,
	js jetstream.JetStream,
	facts ...devices.PendingDeviceFact,
) []string {
	t.Helper()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	outbox.enqueue(facts...)
	relay, err := startDeviceFactRelay(
		outbox, testDeviceFactValidator(t), discardLogger(), jsDeviceFactPublisher(js), testDeviceFactRelayOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), testFactLiveness)
		defer cancelDrain()
		_ = relay.Drain(drainContext)
	})
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == len(facts) })
	identities := make([]string, 0, len(facts))
	for _, item := range facts {
		identities = append(identities, string(pendingFactID(item.Fact)))
	}
	return identities
}

// testTailFacts builds count distinct pending Observation facts with strictly
// increasing observed and commit times, so stream order is unambiguous.
func testTailFacts(t *testing.T, count int) []devices.PendingDeviceFact {
	t.Helper()
	entityID := mustEntityID(t)
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	facts := make([]devices.PendingDeviceFact, 0, count)
	for index := range count {
		facts = append(facts, pendingFact(int64(index+1), testObservationFact(
			t,
			entityID,
			observedAt.Add(time.Duration(index)*time.Minute),
			`"fact-`+string(rune('a'+index))+`"`,
			devices.DispositionApplied,
		)))
	}
	return facts
}

func createTestDeviceFactConsumer(
	t *testing.T,
	stream jetstream.Stream,
	name string,
	policy jetstream.DeliverPolicy,
) jetstream.Consumer {
	t.Helper()
	consumer, err := stream.CreateConsumer(context.Background(), jetstream.ConsumerConfig{
		Name:          name,
		Durable:       name,
		DeliverPolicy: policy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: natswire.DeviceFactWildcard(),
	})
	if err != nil {
		t.Fatalf("create device fact consumer %q: %v", name, err)
	}
	return consumer
}

// fetchTestDeviceFactIDs pulls up to max messages, acknowledges each and returns
// the published fact identities. A short maximum wait keeps a missing message a
// bounded failure instead of a hang.
func fetchTestDeviceFactIDs(t *testing.T, consumer jetstream.Consumer, limit int) []string {
	t.Helper()
	batch, err := consumer.Fetch(limit, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	identities := make([]string, 0, limit)
	for message := range batch.Messages() {
		identities = append(identities, publishedDeviceFactID(t, message.Data()))
		if ackErr := message.Ack(); ackErr != nil {
			t.Fatal(ackErr)
		}
	}
	if len(identities) == 0 {
		t.Fatalf("consumer delivered no device fact within the wait budget: %v", batch.Error())
	}
	return identities
}

func publishedDeviceFactID(t *testing.T, payload []byte) string {
	t.Helper()
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode published device fact: %v", err)
	}
	return envelope.ID
}
