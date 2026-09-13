package ecowitt //nolint:testpackage // Transport tests exercise the package-private MQTT seam.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// fillRelayUntilOverflow pushes numbered deliveries through push until the
// bounded relay rejects one, and returns the number of admitted deliveries. A
// caller can then check that every admitted delivery drains in order and that
// the rejected one never arrived.
func fillRelayUntilOverflow(refused <-chan struct{}, push func(sequence int)) int {
	admitted := 0
	for {
		if admitted > 3*mqttRelayCapacity {
			return -1
		}
		push(admitted)
		select {
		case <-refused:
			return admitted
		default:
			admitted++
		}
	}
}

// TestMQTTRelayBoundsAndSignalsOverflowWithoutEvicting protects the bounded
// callback relay: a full queue signals overflow instead of blocking a Paho
// callback or evicting an admitted delivery, and every admitted delivery is
// still delivered in arrival order before the drain exits.
func TestMQTTRelayBoundsAndSignalsOverflowWithoutEvicting(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	delivered := make(chan mqttMessage, 3*mqttRelayCapacity)
	relay := newMQTTRelay(func(message mqttMessage) {
		<-gate
		delivered <- message
	})
	admitted := fillRelayUntilOverflow(relay.overflowed(), func(sequence int) {
		relay.enqueue(mqttMessage{Payload: []byte(fmt.Sprintf("message-%03d", sequence))})
	})
	if admitted < 0 {
		t.Fatal("the relay never signalled overflow")
	}
	// The seal happens while the queue is full, so the bound is reached, plus at
	// most the one delivery the drain already dequeued.
	if admitted < mqttRelayCapacity || admitted > mqttRelayCapacity+1 {
		t.Fatalf("overflow signalled after %d admissions, want the %d-entry bound plus at most one dequeued delivery",
			admitted, mqttRelayCapacity)
	}

	close(gate)
	relay.wait()
	close(delivered)
	if got := len(delivered); got != admitted {
		t.Fatalf("delivered %d messages, want the %d admitted before overflow", got, admitted)
	}
	for index := range admitted {
		message := <-delivered
		want := fmt.Sprintf("message-%03d", index)
		if string(message.Payload) != want {
			t.Fatalf("delivered[%d] = %q, want %q: the relay evicted or reordered an admitted delivery",
				index, message.Payload, want)
		}
	}
}

// TestMQTTRelaySealDrainsAdmittedDeliveriesAndIgnoresLateOnes protects the
// connection-loss contract: sealing stops new admissions but every admitted
// delivery is still delivered in order, and a callback that arrives after the
// seal — including one after the drain exited — is ignored instead of
// published.
func TestMQTTRelaySealDrainsAdmittedDeliveriesAndIgnoresLateOnes(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	delivered := make(chan mqttMessage, 4)
	relay := newMQTTRelay(func(message mqttMessage) {
		<-gate
		delivered <- message
	})
	relay.enqueue(mqttMessage{Payload: []byte("first")})
	relay.enqueue(mqttMessage{Payload: []byte("second")})

	relay.seal()
	relay.enqueue(mqttMessage{Payload: []byte("late")})
	close(gate)
	relay.wait()

	relay.enqueue(mqttMessage{Payload: []byte("post-drain")})
	close(delivered)
	sequence := []string{}
	for message := range delivered {
		sequence = append(sequence, string(message.Payload))
	}
	want := []string{"first", "second"}
	if !slices.Equal(sequence, want) {
		t.Fatalf("delivered %v, want the admitted deliveries %v in order", sequence, want)
	}
}

// TestMQTTRelayOverflowSignalsLossImmediatelyAndStillDrains protects the
// overflow contract: a saturated relay surfaces a connection loss promptly —
// without waiting for the admitted backlog to drain — and then still delivers
// every admitted delivery in arrival order.
func TestMQTTRelayOverflowSignalsLossImmediatelyAndStillDrains(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	delivered := make(chan mqttMessage, 3*mqttRelayCapacity)
	relay := newMQTTRelay(func(message mqttMessage) {
		<-gate
		delivered <- message
	})
	lost := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go relay.watchOverflow(ctx, lost)

	admitted := fillRelayUntilOverflow(relay.overflowed(), func(sequence int) {
		relay.enqueue(mqttMessage{Payload: []byte(fmt.Sprintf("message-%03d", sequence))})
	})
	if admitted < 0 {
		t.Fatal("a saturated relay never signalled overflow")
	}
	select {
	case err := <-lost:
		if !errors.Is(err, errMQTTRelayOverflow) {
			t.Fatalf("relay loss = %v, want relay overflow", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a saturated relay never reported a connection loss")
	}

	// The loss was signalled while the drain was still blocked, so no admitted
	// delivery had been handed off yet: the signal never waits for the backlog.
	select {
	case message := <-delivered:
		t.Fatalf("delivered %q before the overflow loss was signalled", message.Payload)
	default:
	}

	close(gate)
	relay.wait()
	close(delivered)
	if got := len(delivered); got != admitted {
		t.Fatalf("delivered %d messages, want the %d admitted before overflow", got, admitted)
	}
	for index := range admitted {
		message := <-delivered
		want := fmt.Sprintf("message-%03d", index)
		if string(message.Payload) != want {
			t.Fatalf("delivered[%d] = %q, want %q after overflow", index, message.Payload, want)
		}
	}
}

// TestPahoConnectionCloseSealsAndWaitsForAdmittedDeliveries protects the
// production transport contract that makes the drain ordering possible: Close
// seals the relay first, waits until every admitted delivery has been handed
// over, and only then disconnects. It fails if Close returns while a copied
// delivery is still queued, which is how a Paho connection loss dropped copied
// reports.
func TestPahoConnectionCloseSealsAndWaitsForAdmittedDeliveries(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	delivered := make(chan mqttMessage, 2)
	relay := newMQTTRelay(func(message mqttMessage) {
		<-gate
		delivered <- message
	})
	client := paho.NewClient(paho.NewClientOptions().AddBroker("tcp://127.0.0.1:1883"))
	connection := &pahoConnection{client: client, relay: relay, lost: make(chan error, 1)}
	relay.enqueue(mqttMessage{Payload: []byte("first")})
	relay.enqueue(mqttMessage{Payload: []byte("second")})

	closed := make(chan struct{})
	go func() {
		connection.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while an admitted delivery was still queued")
	case <-time.After(100 * time.Millisecond):
	}

	close(gate)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the admitted deliveries drained")
	}
	close(delivered)
	sequence := []string{}
	for message := range delivered {
		sequence = append(sequence, string(message.Payload))
	}
	if want := []string{"first", "second"}; !slices.Equal(sequence, want) {
		t.Fatalf("delivered %v, want the admitted deliveries %v in order", sequence, want)
	}

	// Close is idempotent so a failed generation and connection cleanup can both
	// call it.
	connection.Close()
}

// startRelayBackedGeneration starts one MQTT generation through the production
// connection runner on a deliberately one-event coordinator queue, so a test can
// stop consuming and force the transport relay to hold a backlog of admitted
// deliveries exactly as real coordinator backpressure would. It returns the
// queue and the runner's result channel once the generation announcement and its
// SUBACK are acknowledged.
func startRelayBackedGeneration(
	t *testing.T,
	harness *runtimeHarness,
) (chan runtimeEvent, chan error) {
	t.Helper()
	events := make(chan runtimeEvent, 1)
	harness.coordinator.events = events
	runResult := make(chan error, 1)
	go func() {
		runResult <- harness.adapter.runConnection(harness.coordinator.ctx, harness.coordinator, 1)
	}()
	starting := receiveRuntimeEvent(t, events)
	if _, ok := starting.(generationStarting); !ok {
		t.Fatalf("first coordinator event = %T, want the generation announcement", starting)
	}
	if err := harness.coordinator.handle(starting); err != nil {
		t.Fatalf("handle generationStarting: %v", err)
	}
	established := receiveRuntimeEvent(t, events)
	if _, ok := established.(generationEstablished); !ok {
		t.Fatalf("second coordinator event = %T, want the SUBACK announcement", established)
	}
	if err := harness.coordinator.handle(established); err != nil {
		t.Fatalf("handle generationEstablished: %v", err)
	}
	return events, runResult
}

// receiveRuntimeEvent waits for one coordinator event, failing after a bounded
// wait so a stuck relay or connection surfaces as a test failure rather than a
// hang.
func receiveRuntimeEvent(t *testing.T, events <-chan runtimeEvent) runtimeEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a coordinator event")
		return nil
	}
}

// TestRunConnectionDrainsRelayBacklogBeforeUpstreamUnavailable protects the
// evidence ordering between the transport relay and the coordinator: a
// connection lost with copied deliveries still queued must hand every admitted
// delivery to the coordinator before the generation is reported unavailable, so
// an accepted report is never dropped by a Paho connection loss. The test drives
// the production connection runner and mirrors the supervisor's sequence.
func TestRunConnectionDrainsRelayBacklogBeforeUpstreamUnavailable(t *testing.T) {
	t.Parallel()

	const backlog = 8
	harness := newRuntimeHarness(t, nil)
	events, runResult := startRelayBackedGeneration(t, harness)

	receivedAt := harness.clock.Now()
	payloads := make([]string, 0, backlog)
	for sequence := range backlog {
		payload := fixtureReportMessage(t, sequence)
		payloads = append(payloads, string(payload))
		harness.dialer.push(mqttMessage{Topic: fixtureTopic, Payload: payload, ReceivedAt: receivedAt})
	}

	loss := errors.New("sentinel broker connection loss")
	harness.dialer.connection.lost <- loss

	// The generation must not end while admitted deliveries are still queued in
	// the relay: the runner waits for the drain before it returns.
	select {
	case err := <-runResult:
		t.Fatalf("the connection runner returned before draining admitted deliveries: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	recorded := make([]runtimeEvent, 0, backlog+1)
	for index, want := range payloads {
		received, ok := receiveRuntimeEvent(t, events).(reportReceived)
		if !ok {
			t.Fatalf("event %d is not a report delivery: the relay dropped or reordered an admitted delivery", index)
		}
		if received.generation != 1 {
			t.Fatalf("report %d generation = %d, want the lost generation 1", index, received.generation)
		}
		if string(received.message.Payload) != want {
			t.Fatalf("report %d payload = %q, want %q in arrival order",
				index, received.message.Payload, want)
		}
		recorded = append(recorded, received)
	}

	select {
	case err := <-runResult:
		if !errors.Is(err, loss) {
			t.Fatalf("connection runner error = %v, want the connection loss", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the connection runner did not return after the relay drained")
	}

	// The supervisor reports the generation unavailable only after the runner
	// returned, which is after every admitted report reached the coordinator.
	if err := harness.adapter.reportUpstreamUnavailable(
		harness.coordinator.ctx, harness.coordinator, 1, loss,
	); err != nil {
		t.Fatalf("report upstream unavailable: %v", err)
	}
	recorded = append(recorded, receiveRuntimeEvent(t, events))
	if _, ok := recorded[len(recorded)-1].(upstreamUnavailable); !ok {
		t.Fatalf("final event = %T, want the unavailable transition after every admitted report",
			recorded[len(recorded)-1])
	}

	// Replaying the recorded order through a full-size queue proves the
	// coordinator accepted every admitted report instead of dropping one on the
	// loss.
	harness.coordinator.events = make(chan runtimeEvent, runtimeEventBuffer)
	for _, event := range recorded {
		if err := harness.coordinator.submit(harness.coordinator.ctx, event); err != nil {
			t.Fatalf("replay %T: %v", event, err)
		}
	}
	harness.pump(t)
	if harness.terminal != nil {
		t.Fatalf("coordinator terminal error: %v", harness.terminal)
	}
	snapshot := snapshotFor(t, testConfig(t))
	if got, want := len(harness.session.published()), backlog*len(snapshot.entities); got != want {
		t.Fatalf("published Observations = %d, want every admitted report's %d", got, want)
	}
	if !hasUnhealthy(harness.session.health(), externalSystemUnavailableReason) {
		t.Fatalf("health = %#v, want the unavailable transition after the drained reports",
			harness.session.health())
	}
}

// TestRunConnectionRelayOverflowEndsGenerationAndDrainsAdmittedDeliveries
// protects the overflow ordering: a saturated relay seals admission, ends the
// generation with the overflow cause, and still delivers every admitted
// delivery to the coordinator, while the delivery that exceeded the bound never
// reaches it. The rejected delivery is visible as the ended generation rather
// than as a silent drop while the connection stays healthy.
func TestRunConnectionRelayOverflowEndsGenerationAndDrainsAdmittedDeliveries(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	events, runResult := startRelayBackedGeneration(t, harness)
	relay := harness.dialer.connection.relaySnapshot()
	if relay == nil {
		t.Fatal("the generation has no transport relay")
	}

	receivedAt := harness.clock.Now()
	admitted := fillRelayUntilOverflow(relay.overflowed(), func(sequence int) {
		harness.dialer.push(mqttMessage{
			Topic:      fixtureTopic,
			Payload:    fixtureReportMessage(t, sequence),
			ReceivedAt: receivedAt,
		})
	})
	if admitted < 0 {
		t.Fatal("the relay never signalled overflow")
	}
	// The bound plus the at most two deliveries the drain already dequeued: one
	// waiting in the one-event coordinator queue and one blocked submitting.
	if admitted < mqttRelayCapacity || admitted > mqttRelayCapacity+2 {
		t.Fatalf("overflow after %d admissions, want the %d-entry bound plus at most two dequeued deliveries",
			admitted, mqttRelayCapacity)
	}

	recorded := make([]runtimeEvent, 0, admitted+1)
	for index := range admitted {
		received, ok := receiveRuntimeEvent(t, events).(reportReceived)
		if !ok {
			t.Fatalf("event %d is not a report delivery: the relay dropped an admitted delivery", index)
		}
		want := string(fixtureReportMessage(t, index))
		if string(received.message.Payload) != want {
			t.Fatalf("report %d payload = %q, want %q in arrival order",
				index, received.message.Payload, want)
		}
		recorded = append(recorded, received)
	}

	select {
	case err := <-runResult:
		if !errors.Is(err, errMQTTRelayOverflow) {
			t.Fatalf("connection runner error = %v, want the relay overflow", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a saturated relay did not end the generation")
	}

	if err := harness.adapter.reportUpstreamUnavailable(
		harness.coordinator.ctx, harness.coordinator, 1, errMQTTRelayOverflow,
	); err != nil {
		t.Fatalf("report upstream unavailable: %v", err)
	}
	recorded = append(recorded, receiveRuntimeEvent(t, events))
	if _, ok := recorded[len(recorded)-1].(upstreamUnavailable); !ok {
		t.Fatalf("final event = %T, want the unavailable transition after every admitted delivery",
			recorded[len(recorded)-1])
	}
}

// TestPahoClientOptionsPinMQTT311CleanSession protects the MQTT 3.1.1 clean
// session, deterministic client ID, and Adapter-owned reconnect contract. It
// fails if a Paho default silently enables reconnects or changes the wire
// protocol.
func TestPahoClientOptionsPinMQTT311CleanSession(t *testing.T) {
	t.Parallel()

	options := newPahoClientOptions(mqttConfig{
		URL:      "mqtt://127.0.0.1:1883",
		ClientID: "hearth-eco-0123456789ab",
	}, func(error) {})
	if len(options.Servers) != 1 || options.Servers[0].String() != "tcp://127.0.0.1:1883" {
		t.Fatalf("brokers = %v, want one normalized tcp broker", options.Servers)
	}
	if options.ClientID != "hearth-eco-0123456789ab" {
		t.Fatalf("client ID = %q", options.ClientID)
	}
	if len(options.ClientID) != 23 {
		t.Fatalf("client ID length = %d, want the deterministic 23-character ID", len(options.ClientID))
	}
	if options.ProtocolVersion != mqttProtocolVersion311 {
		t.Fatalf("protocol version = %d, want MQTT 3.1.1 level 4", options.ProtocolVersion)
	}
	if !options.CleanSession {
		t.Fatal("clean session is disabled")
	}
	if options.AutoReconnect {
		t.Fatal("automatic reconnect is enabled")
	}
	if options.ConnectRetry {
		t.Fatal("initial connect retry is enabled")
	}
	if !options.Order {
		t.Fatal("ordered callback ingestion is disabled")
	}
}

// TestNormalizePahoBrokerURL protects the mqtt:// to tcp:// normalization.
func TestNormalizePahoBrokerURL(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct{ given, want string }{
		{"mqtt://127.0.0.1:1883", "tcp://127.0.0.1:1883"},
		{"tcp://127.0.0.1:1883", "tcp://127.0.0.1:1883"},
		{"mqtt://broker.internal:1883", "tcp://broker.internal:1883"},
	} {
		t.Run(testCase.given, func(t *testing.T) {
			t.Parallel()
			if got := normalizePahoBrokerURL(testCase.given); got != testCase.want {
				t.Fatalf("normalized %q = %q, want %q", testCase.given, got, testCase.want)
			}
		})
	}
}

// TestWaitPahoTokenHonorsContext protects bounded token waits: connect,
// subscribe, and publish can never wait past context expiry.
func TestWaitPahoTokenHonorsContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitPahoToken(ctx, stalledPahoToken{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context cancellation", err)
	}
}

// TestSubscriptionRejectedInspectsSubackFailure protects the MQTT 3.1.1 SUBACK
// 0x80 contract: Paho reports a broker rejection in the SubscribeToken result,
// not in Token.Error, so an unchecked result would leave the Adapter believing
// it holds a subscription it does not have. The fixed diagnostic never repeats
// the rejected topic.
func TestSubscriptionRejectedInspectsSubackFailure(t *testing.T) {
	t.Parallel()

	// secretTopic is a distinctive placeholder that can never collide with the
	// fixed diagnostic text.
	const secretTopic = "ecowitt/zzsecretzzzzz"
	rejected := fakeSubscribeToken{
		result: map[string]byte{secretTopic: mqttSubscribeFailureReturnCode},
	}
	err := subscriptionRejected(rejected)
	if !errors.Is(err, errMQTTSubscribeRejected) {
		t.Fatalf("rejected subscription error = %v, want errMQTTSubscribeRejected", err)
	}
	if strings.Contains(err.Error(), secretTopic) {
		t.Fatalf("rejection diagnostic repeated the topic: %s", err)
	}
	granted := fakeSubscribeToken{result: map[string]byte{secretTopic: 1}}
	if grantedErr := subscriptionRejected(granted); grantedErr != nil {
		t.Fatalf("granted subscription error = %v, want nil", grantedErr)
	}
	if noResultErr := subscriptionRejected(stalledPahoToken{}); noResultErr != nil {
		t.Fatalf("token without a result error = %v, want nil", noResultErr)
	}
}

// fakeSubscribeToken is a completed SubscribeToken-shaped result for tests.
type fakeSubscribeToken struct {
	result map[string]byte
}

// Wait implements paho.Token.
func (fakeSubscribeToken) Wait() bool { return true }

// WaitTimeout implements paho.Token.
func (fakeSubscribeToken) WaitTimeout(time.Duration) bool { return true }

// Done implements paho.Token.
func (fakeSubscribeToken) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

// Error implements paho.Token.
func (fakeSubscribeToken) Error() error { return nil }

// Result reports the broker return code for every requested filter.
func (token fakeSubscribeToken) Result() map[string]byte { return token.result }

var _ paho.Token = fakeSubscribeToken{}

// TestReceivedAtPreservesMonotonicReading protects that the callback receipt
// time keeps Go's monotonic reading, so a wall-clock step cannot move a report
// or measurement deadline. Only the SDK wire timestamp fields are UTC.
func TestReceivedAtPreservesMonotonicReading(t *testing.T) {
	t.Parallel()

	received := receivedAtNow()
	if !hasMonotonic(received) {
		t.Fatal("receipt time lost its Go monotonic reading")
	}
}

// hasMonotonic reports whether a time value keeps Go's monotonic reading. It
// compares the struct deliberately: [time.Time.Equal] ignores the monotonic
// reading and cannot detect its presence.
func hasMonotonic(value time.Time) bool {
	return value.Round(0) != value
}

// stalledPahoToken never completes.
type stalledPahoToken struct{}

// Wait implements paho.Token.
func (stalledPahoToken) Wait() bool { return false }

// WaitTimeout implements paho.Token.
func (stalledPahoToken) WaitTimeout(time.Duration) bool { return false }

// Done implements paho.Token.
func (stalledPahoToken) Done() <-chan struct{} { return make(chan struct{}) }

// Error implements paho.Token.
func (stalledPahoToken) Error() error { return nil }

var _ paho.Token = stalledPahoToken{}
