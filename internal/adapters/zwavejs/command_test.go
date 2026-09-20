package zwavejs //nolint:testpackage // Command tests exercise the private coordinator and connection seam.

// command_test.go covers D4: exact SetValue translation, the successful-status
// gate, fresh correlated poll evidence, FIFO and concurrency, deadline
// consumption, disconnect races, and the absence of duplicate responses or
// ordinary/linked double publication.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// startCommandRuntime reconciles one node and returns the pieces a Command test
// needs.
func startCommandRuntime(
	t *testing.T,
	node nodeState,
) (*runtimeRecorder, *runtimeSession, *fakeConnection, *Adapter) {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	return recorder, session, connection, zwave
}

// runCommand submits one Command and waits for its single handler result.
func runCommand(
	t *testing.T,
	zwave *Adapter,
	command adapter.Command,
	responder adapter.Responder,
) error {
	t.Helper()
	result := submitCommand(t, zwave, command, responder)
	select {
	case err := <-result:
		return err
	case <-time.After(harnessTimeout):
		t.Fatal("Command did not respond")
		return nil
	}
}

// plannedCommandCase is one expected Command translation.
type plannedCommandCase struct {
	name                 string
	node                 nodeState
	entityKey            string
	parameters           string
	wantAction           int
	wantValue            string
	wantCurrent          int
	snapshotObservations int
}

func TestCommandPublishesExactPlannedSetValue(t *testing.T) {
	t.Parallel()
	cases := []plannedCommandCase{
		{
			name:        "binary power on",
			node:        switchNodeFixture(testNodeID, "Kitchen Switch"),
			entityKey:   "power",
			parameters:  `{"value":true}`,
			wantAction:  commandClassBinarySwitch,
			wantValue:   "true",
			wantCurrent: commandClassBinarySwitch,
			// One Binary Switch power Entity publishes one snapshot Observation.
			snapshotObservations: 1,
		},
		{
			name:                 "binary power off",
			node:                 switchNodeFixture(testNodeID, "Kitchen Switch"),
			entityKey:            "power",
			parameters:           `{"value":false}`,
			wantAction:           commandClassBinarySwitch,
			wantValue:            "false",
			wantCurrent:          commandClassBinarySwitch,
			snapshotObservations: 1,
		},
		{
			name:                 "multilevel brightness",
			node:                 dimmerNodeFixture(testNodeID, "Hallway Dimmer"),
			entityKey:            "brightness",
			parameters:           `{"value":40}`,
			wantAction:           commandClassMultilevelSwitch,
			wantValue:            "40",
			wantCurrent:          commandClassMultilevelSwitch,
			snapshotObservations: 2,
		},
		{
			name:                 "multilevel power on",
			node:                 dimmerNodeFixture(testNodeID, "Hallway Dimmer"),
			entityKey:            "power",
			parameters:           `{"value":true}`,
			wantAction:           commandClassMultilevelSwitch,
			wantValue:            "255",
			wantCurrent:          commandClassMultilevelSwitch,
			snapshotObservations: 2,
		},
		{
			name:                 "multilevel power off",
			node:                 dimmerNodeFixture(testNodeID, "Hallway Dimmer"),
			entityKey:            "power",
			parameters:           `{"value":false}`,
			wantAction:           commandClassMultilevelSwitch,
			wantValue:            "0",
			wantCurrent:          commandClassMultilevelSwitch,
			snapshotObservations: 2,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			recorder, session, connection, zwave := startCommandRuntime(t, testCase.node)
			entityID := routeEntityID(testNodeID, testCase.entityKey)
			responder := newFakeResponder(recorder, session)

			if err := runCommand(
				t,
				zwave,
				commandFixture(entityID, testCase.parameters, time.Now().Add(time.Minute)),
				responder,
			); err != nil {
				t.Fatalf("Command error: %v", err)
			}
			waitFor(t, "linked poll evidence", func() bool {
				return len(connection.recordedPollCalls()) == 1 &&
					len(session.recordedLinked()) == 1
			})
			assertPlannedWrite(t, connection, testCase)
			assertPlannedPoll(t, connection, testCase.wantCurrent)
			if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
				t.Fatalf("responses = accepted %d rejected %d total %d, want 1/0/1", accepted, rejected, total)
			}
			linked := session.recordedLinked()
			if linked[0].EntityID != entityID {
				t.Fatalf("linked Entity = %s, want %s", linked[0].EntityID, entityID)
			}
			// A poll result is linked evidence, never an ordinary Observation: the
			// only ordinary Observations are the snapshot Observations published
			// during reconciliation, and no further State report exists.
			if got := len(session.recordedObservations()); got != testCase.snapshotObservations {
				t.Fatalf("ordinary Observations = %d, want %d snapshot Observations",
					got, testCase.snapshotObservations)
			}
		})
	}
}

// assertPlannedWrite asserts the exact node, Value ID, and value one Command
// wrote upstream.
func assertPlannedWrite(t *testing.T, connection *fakeConnection, testCase plannedCommandCase) {
	t.Helper()
	writes := connection.recordedSetCalls()
	if len(writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writes))
	}
	write := writes[0]
	if write.NodeID != testNodeID {
		t.Fatalf("write node = %d, want %d", write.NodeID, testNodeID)
	}
	if write.ValueID.CommandClass != testCase.wantAction ||
		write.ValueID.Endpoint != 0 || write.ValueID.Property.Name != valuePropertyTargetValue {
		t.Fatalf("write Value ID = %+v, want cc %d endpoint 0 targetValue",
			write.ValueID, testCase.wantAction)
	}
	if write.Value != testCase.wantValue {
		t.Fatalf("write value = %s, want %s", write.Value, testCase.wantValue)
	}
}

// assertPlannedPoll asserts exactly one correlated poll of the planned current
// Value ID.
func assertPlannedPoll(t *testing.T, connection *fakeConnection, wantCommandClass int) {
	t.Helper()
	polls := connection.recordedPollCalls()
	if len(polls) != 1 {
		t.Fatalf("polls = %d, want 1", len(polls))
	}
	if polls[0].CommandClass != wantCommandClass || polls[0].Property.Name != valuePropertyCurrentValue {
		t.Fatalf("poll Value ID = %+v, want cc %d currentValue", polls[0], wantCommandClass)
	}
}

// documentedStatusCase is one node.set_value status outcome.
type documentedStatusCase struct {
	name       string
	status     setValueStatus
	wantAccept bool
	wantPoll   bool
}

func TestCommandAcceptsOnlyDocumentedSuccessfulStatuses(t *testing.T) {
	t.Parallel()
	cases := []documentedStatusCase{
		{name: "success", status: setValueStatusSuccess, wantAccept: true, wantPoll: true},
		{name: "working", status: setValueStatusWorking, wantAccept: true, wantPoll: true},
		{
			name: "success unsupervised", status: setValueStatusSuccessUnsupervised,
			wantAccept: true, wantPoll: true,
		},
		{name: "invalid value refused", status: setValueStatusInvalidValue, wantPoll: false},
		{name: "no device support refused", status: setValueStatusNoDeviceSupport, wantPoll: false},
		{name: "unrecognized status refused", status: setValueStatusUnrecognized, wantPoll: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			recorder, session, connection, zwave := startCommandRuntime(
				t,
				switchNodeFixture(testNodeID, "Kitchen Switch"),
			)
			connection.setValueHook = scriptedStatusHook(testCase)
			responder := newFakeResponder(recorder, session)
			if err := runCommand(
				t,
				zwave,
				commandFixture(routeEntityID(testNodeID, "power"), `{"value":true}`, time.Now().Add(time.Minute)),
				responder,
			); err != nil {
				t.Fatalf("Command error: %v", err)
			}
			if testCase.wantPoll {
				waitFor(t, "linked poll evidence", func() bool {
					return len(connection.recordedPollCalls()) == 1 &&
						len(session.recordedLinked()) == 1
				})
			}
			assertStatusOutcome(t, testCase, responder, session, connection)
		})
	}
}

// assertStatusOutcome asserts the single response and the linked evidence one
// node.set_value status produces. A status the client does not accept as
// success never polls and never publishes linked evidence.
func assertStatusOutcome(
	t *testing.T,
	testCase documentedStatusCase,
	responder *fakeResponder,
	session *runtimeSession,
	connection *fakeConnection,
) {
	t.Helper()
	if accepted, rejected, total := responderCounts(responder); testCase.wantAccept {
		if accepted != 1 || rejected != 0 || total != 1 {
			t.Fatalf("responses = %d/%d/%d, want accepted only", accepted, rejected, total)
		}
	} else if accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want rejected only", accepted, rejected, total)
	}
	if got := connection.recordedPollCalls(); (len(got) == 1) != testCase.wantPoll {
		t.Fatalf("polls = %d, want poll=%t", len(got), testCase.wantPoll)
	}
	if got := len(session.recordedLinked()); (got == 1) != testCase.wantPoll {
		t.Fatalf("linked Observations = %d, want %t", got, testCase.wantPoll)
	}
}

// scriptedStatusHook answers one node.set_value with a documented status. A
// status the client does not accept as success becomes its typed refusal.
func scriptedStatusHook(
	testCase documentedStatusCase,
) func(context.Context, int, valueID, json.RawMessage) (setValueStatus, error) {
	return func(
		context.Context,
		int,
		valueID,
		json.RawMessage,
	) (setValueStatus, error) {
		if !testCase.wantAccept {
			return testCase.status, &setValueRefusedError{Status: testCase.status}
		}
		return testCase.status, nil
	}
}

func TestCommandPollMismatchWaitsForHintsAndEventsAreNeverLinked(t *testing.T) {
	t.Parallel()
	recorder, session, connection, zwave := startCommandRuntime(t, dimmerNodeFixture(23, "Hallway Dimmer"))
	entityID := routeEntityID(testNodeID, "brightness")
	responder := newFakeResponder(recorder, session)
	current := testValueID(commandClassMultilevelSwitch, 0, valuePropertyCurrentValue)

	if err := runCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	waitFor(t, "first linked poll", func() bool {
		return len(session.recordedLinked()) == 1 && len(session.recordedObservations()) == 2
	})

	// A hint whose value does not match the refreshed State is ordinary evidence
	// only, and drives exactly one further poll.
	connection.emit(valueUpdatedEvent(current, "70"))
	waitFor(t, "second linked poll", func() bool {
		return len(session.recordedLinked()) == 2 && len(session.recordedObservations()) == 4
	})

	connection.setNodeState(dimmerNodeAtLevel(23, "40"))
	connection.emit(valueUpdatedEvent(current, "40"))
	waitFor(t, "matching linked poll", func() bool {
		return len(session.recordedLinked()) == 3 && len(session.recordedObservations()) == 6
	})

	if got, want := linkedValues(session.recordedLinked()), []string{"15", "15", "40"}; !equalStrings(got, want) {
		t.Fatalf("linked values = %v, want %v", got, want)
	}
	for _, value := range linkedValues(session.recordedLinked()) {
		if value == "70" {
			t.Fatal("a value update Event was linked as Command evidence")
		}
	}
	if got, want := observationValues(session.recordedObservations()),
		[]string{"true", "15", "true", "70", "true", "40"}; !equalStrings(got, want) {
		t.Fatalf("ordinary Observation values = %v, want %v", got, want)
	}
	if got := len(connection.recordedSetCalls()); got != 1 {
		t.Fatalf("writes = %d, want 1", got)
	}
	if accepted, _, total := responderCounts(responder); accepted != 1 || total != 1 {
		t.Fatalf("responses = accepted %d total %d, want 1/1", accepted, total)
	}

	// A matching linked Observation ends the attempt and releases its FIFO slot.
	second := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		second,
	); err != nil {
		t.Fatalf("second Command error: %v", err)
	}
	if accepted, _, _ := responderCounts(second); accepted != 1 {
		t.Fatalf("second Command accepted = %d, want 1", accepted)
	}
}

func TestCommandCoalescesHintsNoFasterThanThePollInterval(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, dimmerNodeFixture(23, "Hallway Dimmer")),
	)
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	// A long injected interval stands in for the production 250 ms floor, so the
	// assertion stays fast without weakening the invariant.
	const interval = 80 * time.Millisecond
	zwave.pollHintInterval = interval
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	responder := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	waitFor(t, "first linked poll", func() bool { return len(session.recordedLinked()) == 1 })

	current := testValueID(commandClassMultilevelSwitch, 0, valuePropertyCurrentValue)
	connection.emit(valueUpdatedEvent(current, "41"))
	connection.emit(valueUpdatedEvent(current, "42"))
	connection.emit(valueUpdatedEvent(current, "43"))
	waitFor(t, "coalesced second poll", func() bool { return len(session.recordedLinked()) == 2 })

	times := connection.recordedPollTimes()
	if len(times) != 2 {
		t.Fatalf("polls = %d, want 2 coalesced polls", len(times))
	}
	if elapsed := times[1].Sub(times[0]); elapsed < interval {
		t.Fatalf("hint polls %s apart, want at least %s", elapsed, interval)
	}
}

func TestCommandIsFIFOPerNodeAndConcurrentAcrossNodes(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, dimmerNodeFixture(23, "Hallway Dimmer"), dimmerNodeFixture(24, "Porch Dimmer")),
	)
	gate := make(chan struct{})
	connection.setValueHook = func(
		ctx context.Context,
		nodeID int,
		_ valueID,
		_ json.RawMessage,
	) (setValueStatus, error) {
		if nodeID == 23 {
			select {
			case <-gate:
			case <-ctx.Done():
				return setValueStatusUnrecognized, ctx.Err()
			}
		}
		return setValueStatusSuccess, nil
	}
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	firstResponder := newFakeResponder(recorder, session)
	secondResponder := newFakeResponder(recorder, session)
	thirdResponder := newFakeResponder(recorder, session)
	first := submitCommand(t, zwave, commandFixture(
		routeEntityID(testNodeID, "brightness"), `{"value":15}`, time.Now().Add(time.Minute),
	), firstResponder)
	waitFor(t, "first write in flight", func() bool { return writeCountFor(connection, 23) == 1 })
	second := submitCommand(t, zwave, commandFixture(
		routeEntityID(testNodeID, "brightness"), `{"value":20}`, time.Now().Add(time.Minute),
	), secondResponder)
	third := submitCommand(t, zwave, commandFixture(
		routeEntityID(24, "brightness"), `{"value":15}`, time.Now().Add(time.Minute),
	), thirdResponder)

	// Node 24 is a different node, so it dispatches while node 23 is blocked,
	// while the second Command of node 23 waits for its node's FIFO slot.
	waitFor(t, "concurrent node dispatch", func() bool {
		return writeCountFor(connection, 24) == 1
	})
	if got := writeCountFor(connection, 23); got != 1 {
		t.Fatalf("node 23 writes while blocked = %d, want 1 (FIFO)", got)
	}
	close(gate)

	for _, result := range []chan error{first, second, third} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("Command error: %v", err)
			}
		case <-time.After(harnessTimeout):
			t.Fatal("Command did not respond")
		}
	}
	if got, want := writeValuesFor(connection, 23), []string{"15", "20"}; !equalStrings(got, want) {
		t.Fatalf("node 23 write values = %v, want %v", got, want)
	}
	for index, responder := range []*fakeResponder{firstResponder, secondResponder, thirdResponder} {
		if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
			t.Fatalf("responder %d responses = %d/%d/%d, want 1/0/1", index, accepted, rejected, total)
		}
	}
}

func TestCommandDeadlineConsumesQueueTime(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, dimmerNodeFixture(23, "Hallway Dimmer")),
	)
	// The first write never completes, so the second Command can only ever wait.
	connection.setValueHook = func(
		ctx context.Context,
		_ int,
		_ valueID,
		_ json.RawMessage,
	) (setValueStatus, error) {
		<-ctx.Done()
		return setValueStatusUnrecognized, ctx.Err()
	}
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	blockedResponder := newFakeResponder(recorder, session)
	expiringResponder := newFakeResponder(recorder, session)
	submitCommand(t, zwave, commandFixture(entityID, `{"value":15}`, time.Now().Add(time.Minute)), blockedResponder)
	waitFor(t, "occupied FIFO slot", func() bool { return len(connection.recordedSetCalls()) == 1 })

	expiring := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":20}`, time.Now().Add(150*time.Millisecond)),
		expiringResponder,
	)
	select {
	case err := <-expiring:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued Command error = %v, want deadline exceeded", err)
		}
	case <-time.After(harnessTimeout):
		t.Fatal("queued Command never reached its deadline")
	}
	if got := writeCountFor(connection, 23); got != 1 {
		t.Fatalf("writes = %d, want 1 (the expired Command never dispatched)", got)
	}
	if _, _, total := responderCounts(expiringResponder); total != 0 {
		t.Fatalf("expired Command responses = %d, want 0", total)
	}
}

func TestCommandDisconnectRejectsUnacceptedAndSilencesAccepted(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, dimmerNodeFixture(23, "Hallway Dimmer")),
	)
	pollEntered := make(chan struct{})
	connection.pollValueHook = func(
		ctx context.Context,
		_ int,
		_ valueID,
	) (json.RawMessage, time.Time, error) {
		close(pollEntered)
		<-ctx.Done()
		return nil, time.Time{}, ctx.Err()
	}
	zwave, dialer := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	responder := newFakeResponder(recorder, session)
	result := submitCommand(
		t,
		zwave,
		commandFixture(routeEntityID(testNodeID, "brightness"), `{"value":40}`, time.Now().Add(time.Minute)),
		responder,
	)
	select {
	case <-pollEntered:
	case <-time.After(harnessTimeout):
		t.Fatal("accepted Command never polled")
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
		t.Fatalf("responses before disconnect = %d/%d/%d, want 1/0/1", accepted, rejected, total)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("accepted Command handler error = %v", err)
		}
	case <-time.After(harnessTimeout):
		t.Fatal("accepted Command handler did not return")
	}

	connection.fail(errors.New("socket closed"))
	waitFor(t, "reconnect dial", func() bool { return dialer.dialCount() >= 2 })

	if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
		t.Fatalf("responses after disconnect = %d/%d/%d, want no second response", accepted, rejected, total)
	}
	if got := len(session.recordedLinked()); got != 0 {
		t.Fatalf("linked Observations = %d, want 0", got)
	}
}

func TestCommandTimedOutSetValueIsAmbiguousAndNeverAccepted(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, switchNodeFixture(23, "Kitchen Switch")),
	)
	// The request reports its own timeout while Hearth's deadline is still open,
	// which is exactly the ambiguous case: the write may have reached the radio.
	release := make(chan struct{})
	connection.setValueHook = func(
		context.Context,
		int,
		valueID,
		json.RawMessage,
	) (setValueStatus, error) {
		<-release
		return setValueStatusUnrecognized, &requestTimeoutError{
			Operation: commandSetValue,
			Cause:     context.DeadlineExceeded,
		}
	}
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	responder := newFakeResponder(recorder, session)
	result := submitCommand(
		t,
		zwave,
		commandFixture(routeEntityID(testNodeID, "power"), `{"value":true}`, time.Now().Add(time.Minute)),
		responder,
	)
	waitFor(t, "write in flight", func() bool { return len(connection.recordedSetCalls()) == 1 })
	close(release)
	err := <-result
	if !isRequestTimeout(err) {
		t.Fatalf("Command error = %v, want a timed-out upstream request", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want an unaccepted, rejected Command", accepted, rejected, total)
	}
	if !session.logs.has("adapter.command_ambiguous") {
		t.Fatalf("logs = %v, want an ambiguous diagnosis", session.logs.events)
	}
	if got := len(session.recordedLinked()); got != 0 {
		t.Fatalf("linked Observations = %d, want 0", got)
	}
}

func TestCommandRejectedParameterRejectionNeverWrites(t *testing.T) {
	t.Parallel()
	recorder, session, connection, zwave := startCommandRuntime(t, switchNodeFixture(23, "Kitchen Switch"))
	responder := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(routeEntityID(testNodeID, "power"), `{"value":"not-a-boolean"}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want a single rejection", accepted, rejected, total)
	}
	if got := len(connection.recordedSetCalls()); got != 0 {
		t.Fatalf("writes = %d, want 0", got)
	}
}

// This test protects the route-invalidation boundary of an accepted Command and
// fails if a node that fell asleep can still satisfy that Command with linked
// poll evidence, or if the sleep produces a second Hearth response.
func TestCommandSleepInvalidatesAcceptedAttemptBeforeLinkedEvidence(t *testing.T) {
	t.Parallel()
	recorder, session, connection, zwave := startCommandRuntime(t, dimmerNodeFixture(23, "Hallway Dimmer"))
	entityID := routeEntityID(testNodeID, "brightness")
	entered := make(chan struct{})
	release := make(chan struct{})
	// The poll is answered with the value the Command asked for, so only route
	// invalidation can prevent linked evidence.
	connection.pollValueHook = func(
		context.Context,
		int,
		valueID,
	) (json.RawMessage, time.Time, error) {
		close(entered)
		<-release
		return json.RawMessage("15"), time.Now().UTC(), nil
	}
	responder := newFakeResponder(recorder, session)
	result := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":15}`, time.Now().Add(time.Minute)),
		responder,
	)
	awaitSignal(t, entered, "the accepted Command to poll")

	connection.emit(nodeEvent(eventSleep))
	waitFor(t, "sleep availability", func() bool {
		report, ok := session.lastAvailability(entityID)
		return ok && report.ReasonCode == nodeAsleepReason
	})
	close(release)
	time.Sleep(testQuietPeriod)

	if got := len(session.recordedLinked()); got != 0 {
		t.Fatalf("linked Observations after sleep = %d, want 0", got)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want a single acceptance and no second response", accepted, rejected, total)
	}
	if err := awaitCommandHandler(t, result); err != nil {
		t.Fatalf("accepted Command error = %v, want nil", err)
	}
	assertHealthyGeneration(t, session)
}

// This test protects an in-flight command-linked publication and fails if a
// route invalidation leaves it running, which would publish linked evidence for
// a route that no longer exists.
func TestCommandSleepCancelsAnInFlightLinkedPublication(t *testing.T) {
	t.Parallel()
	recorder, session, connection, zwave := startCommandRuntime(t, dimmerNodeFixture(23, "Hallway Dimmer"))
	entityID := routeEntityID(testNodeID, "brightness")
	linkedEntered := make(chan struct{})
	linkedCancelled := make(chan struct{})
	session.setLinkedPublishHook(func(ctx context.Context, _ adapter.Observation) error {
		select {
		case linkedEntered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		close(linkedCancelled)
		return ctx.Err()
	})
	responder := newFakeResponder(recorder, session)
	result := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":15}`, time.Now().Add(time.Minute)),
		responder,
	)
	if err := awaitCommandHandler(t, result); err != nil {
		t.Fatalf("accepted Command error = %v, want nil", err)
	}
	awaitSignal(t, linkedEntered, "the linked publication to start")

	connection.emit(nodeEvent(eventSleep))
	awaitSignal(t, linkedCancelled, "the in-flight linked publication to be cancelled")
	time.Sleep(testQuietPeriod)

	if got := len(session.recordedLinked()); got != 0 {
		t.Fatalf("linked Observations after sleep = %d, want 0", got)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want a single acceptance and no second response", accepted, rejected, total)
	}
	assertHealthyGeneration(t, session)
}

// This test protects the absolute deadline of a write that is already on the
// wire and fails if the deadline frees the node's FIFO slot while the write is
// unresolved, or if it loses the ambiguous diagnosis of the write that resolved
// only afterwards.
func TestCommandDeadlineKeepsAnUnresolvedWriteAndDiagnosesItAmbiguous(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, dimmerNodeFixture(23, "Hallway Dimmer")),
	)
	// The upstream never answers the write before the Command deadline.
	connection.setValueHook = func(
		ctx context.Context,
		_ int,
		_ valueID,
		_ json.RawMessage,
	) (setValueStatus, error) {
		<-ctx.Done()
		return setValueStatusUnrecognized, ctx.Err()
	}
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	blockedResponder := newFakeResponder(recorder, session)
	blocked := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":15}`, time.Now().Add(150*time.Millisecond)),
		blockedResponder,
	)
	waitFor(t, "the write to reach the wire", func() bool {
		return len(connection.recordedSetCalls()) == 1
	})
	queuedResponder := newFakeResponder(recorder, session)
	queued := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":20}`, time.Now().Add(time.Minute)),
		queuedResponder,
	)

	// The deadline must diagnose the unresolved write as ambiguous and close the
	// generation rather than release its FIFO slot for the follower.
	waitFor(t, "the unresolved write to be diagnosed", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})
	if !session.logs.has("adapter.command_ambiguous") {
		t.Fatalf("logs = %v, want an ambiguous write diagnosis", session.logs.events)
	}
	if got := writeCountFor(connection, testNodeID); got != 1 {
		t.Fatalf("writes = %d, want 1 behind an unresolved write", got)
	}
	if _, _, total := responderCounts(blockedResponder); total != 0 {
		t.Fatalf("unresolved write responses = %d, want 0 (its deadline already fired)", total)
	}
	if accepted, rejected, total := responderCounts(queuedResponder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("queue follower responses = %d/%d/%d, want a single rejection", accepted, rejected, total)
	}
	if err := awaitCommandHandler(t, blocked); err == nil {
		t.Fatal("the unresolved write reported no error")
	}
	if err := awaitCommandHandler(t, queued); err == nil {
		t.Fatal("the queue follower reported no error")
	}
}

// This test protects the late-result boundary of a write that was already on the
// wire when its deadline passed and fails if a success that arrives afterwards is
// accepted as Command evidence instead of being diagnosed as ambiguous.
func TestCommandLateSetValueSuccessIsNeverAccepted(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, dimmerNodeFixture(23, "Hallway Dimmer")),
	)
	release := make(chan struct{})
	// The upstream answers with a documented success, but only after the
	// Command's absolute deadline has passed.
	connection.setValueHook = func(
		context.Context,
		int,
		valueID,
		json.RawMessage,
	) (setValueStatus, error) {
		<-release
		return setValueStatusSuccess, nil
	}
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	responder := newFakeResponder(recorder, session)
	result := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":15}`, time.Now().Add(100*time.Millisecond)),
		responder,
	)
	waitFor(t, "the write to reach the wire", func() bool {
		return len(connection.recordedSetCalls()) == 1
	})
	time.Sleep(500 * time.Millisecond)
	close(release)

	waitFor(t, "the late write to be diagnosed", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})
	if !session.logs.has("adapter.command_ambiguous") {
		t.Fatalf("logs = %v, want an ambiguous write diagnosis", session.logs.events)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 0 || total != 0 {
		t.Fatalf("responses = %d/%d/%d, want no response at all", accepted, rejected, total)
	}
	if got := len(session.recordedLinked()); got != 0 {
		t.Fatalf("linked Observations = %d, want 0", got)
	}
	if err := awaitCommandHandler(t, result); err == nil {
		t.Fatal("the late write reported no error")
	}
}

// This test protects the absolute-deadline boundary of Acceptance and fails if a
// successful node.set_value that the coordinator processes at or after the
// Command's deadline is accepted only because its completion event reached the
// queue before the deadline-timer event.
func TestCommandLateSuccessBeforeTheTimerEventIsStillAmbiguous(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, dimmerNodeFixture(23, "Hallway Dimmer")),
	)
	release := make(chan struct{})
	// The upstream answers with a documented success, but only once the test has
	// queued that completion behind a blocked coordinator.
	connection.setValueHook = func(
		context.Context,
		int,
		valueID,
		json.RawMessage,
	) (setValueStatus, error) {
		<-release
		return setValueStatusSuccess, nil
	}
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	boundary := time.Now().Add(2 * time.Second)
	responder := newFakeResponder(recorder, session)
	late := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":15}`, boundary),
		responder,
	)
	waitFor(t, "the write to reach the wire", func() bool {
		return len(connection.recordedSetCalls()) == 1
	})

	// The serial coordinator blocks inside one synchronous rejection, so the
	// write's success completion and the deadline timer both queue while it is
	// busy. The completion is queued first, so it is the event that is processed
	// first, at a wall-clock time past the deadline.
	blocker := &blockingResponder{entered: make(chan struct{}, 1), release: make(chan struct{})}
	blocked := submitCommand(t, zwave, commandFixture(
		"ent-does-not-exist", `{"value":true}`, time.Now().Add(time.Minute),
	), blocker)
	select {
	case <-blocker.entered:
	case <-time.After(harnessTimeout):
		t.Fatal("the coordinator never blocked on a synchronous rejection")
	}
	if !time.Now().Before(boundary) {
		t.Fatal("the test could not block the coordinator before the deadline")
	}
	close(release)
	for time.Now().Before(boundary) {
		time.Sleep(time.Millisecond)
	}
	close(blocker.release)
	if err := awaitCommandHandler(t, blocked); err != nil {
		t.Fatalf("the synchronous rejection returned %v, want a plain rejection", err)
	}

	if err := awaitCommandHandler(t, late); err == nil {
		t.Fatal("the late success reported no error")
	}
	waitFor(t, "the late success to be diagnosed", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})
	if !session.logs.has("adapter.command_ambiguous") {
		t.Fatalf("logs = %v, want an ambiguous write diagnosis", session.logs.events)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 0 || total != 0 {
		t.Fatalf("responses = %d/%d/%d, want no response at all", accepted, rejected, total)
	}
	if got := len(session.recordedLinked()); got != 0 {
		t.Fatalf("linked Observations = %d, want 0", got)
	}
	if got := writeCountFor(connection, testNodeID); got != 1 {
		t.Fatalf("writes = %d, want 1 (no dispatch past the unresolved write)", got)
	}
}

// blockingResponder blocks the coordinator's synchronous rejection path until a
// test releases it, so a test can queue other coordinator events behind one
// transition deterministically.
type blockingResponder struct {
	entered chan struct{}
	release chan struct{}
}

func (*blockingResponder) Accept() (adapter.CommandEvidence, error) {
	return nil, adapter.ErrAlreadyResponded
}

func (*blockingResponder) Reject(string) error { return nil }

func (responder *blockingResponder) RejectUnavailable(string) error {
	select {
	case responder.entered <- struct{}{}:
	default:
	}
	select {
	case <-responder.release:
	case <-time.After(harnessTimeout):
	}
	return nil
}

// This test protects the Command-deadline poll boundary and fails if an
// unanswered poll keeps its goroutine and waiter for the connection's lifetime,
// or if the deadline of that poll closes a healthy generation.
func TestCommandDeadlineReleasesUnansweredPolls(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(
			testHomeID,
			dimmerNodeFixture(23, "Hallway Dimmer"),
			dimmerNodeFixture(24, "Porch Dimmer"),
			dimmerNodeFixture(25, "Side Dimmer"),
		),
	)
	var released atomic.Int64
	// The upstream never answers these polls, so only the Command deadline can
	// release them. Each poll returns its own context error, exactly as the
	// production client does for an abandoned correlation.
	connection.pollValueHook = func(
		ctx context.Context,
		_ int,
		_ valueID,
	) (json.RawMessage, time.Time, error) {
		<-ctx.Done()
		released.Add(1)
		return nil, time.Time{}, ctx.Err()
	}
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	entityID := func(nodeID int) string { return routeEntityID(nodeID, "brightness") }
	for _, nodeID := range []int{23, 24, 25} {
		responder := newFakeResponder(recorder, session)
		result := submitCommand(
			t,
			zwave,
			commandFixture(entityID(nodeID), `{"value":40}`, time.Now().Add(150*time.Millisecond)),
			responder,
		)
		if err := awaitCommandHandler(t, result); err != nil {
			t.Fatalf("node %d Command error = %v", nodeID, err)
		}
	}
	waitFor(t, "every unanswered poll to return at its deadline", func() bool {
		return released.Load() == 3
	})
	if got := len(connection.recordedPollCalls()); got != 3 {
		t.Fatalf("polls = %d, want one per Command", got)
	}
	// The generation absorbed three deadline-abandoned polls and keeps serving.
	responder := newFakeResponder(recorder, session)
	result := submitCommand(
		t,
		zwave,
		commandFixture(entityID(23), `{"value":40}`, time.Now().Add(time.Minute)),
		responder,
	)
	if err := awaitCommandHandler(t, result); err != nil {
		t.Fatalf("Command after deadline polls = %v, want the generation still usable", err)
	}
	if _, _, total := responderCounts(responder); total != 1 {
		t.Fatalf("later Command responses = %d, want a single acceptance", total)
	}
	assertHealthyGeneration(t, session)
}

// writeCountFor counts the recorded writes of one node.
func writeCountFor(connection *fakeConnection, nodeID int) int {
	count := 0
	for _, call := range connection.recordedSetCalls() {
		if call.NodeID == nodeID {
			count++
		}
	}
	return count
}

// writeValuesFor returns the recorded upstream values of one node in order.
func writeValuesFor(connection *fakeConnection, nodeID int) []string {
	values := make([]string, 0)
	for _, call := range connection.recordedSetCalls() {
		if call.NodeID == nodeID {
			values = append(values, call.Value)
		}
	}
	return slices.Clip(values)
}

// linkedValues renders the typed State of every linked Observation in order.
func linkedValues(observations []adapter.Observation) []string {
	return observationValues(observations)
}

// startScriptedAdapter runs one Adapter over the production WebSocket client
// against a scripted server, so a regression test crosses the real client
// correlation rule instead of only the fake connection seam.
func startScriptedAdapter(
	t *testing.T,
	server *scriptedServer,
) (*runtimeSession, *runtimeRecorder, *Adapter) {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	zwave, err := newAdapter(
		session,
		Config{URL: server.url},
		slog.New(session.logs),
		websocketDialer{},
	)
	if err != nil {
		t.Fatal(err)
	}
	zwave.retryDelay = func(time.Duration) time.Duration { return time.Millisecond }
	zwave.pollHintInterval = harnessPollHintInterval
	startRuntime(t, zwave)
	return session, recorder, zwave
}

// setValueSuccessResult is the schema-29 double-wrapped successful status.
func setValueSuccessResult() map[string]any {
	return map[string]any{"result": map[string]any{"status": setValueStatusSuccess}}
}

// awaitSignal waits for one bounded scripted-server signal.
func awaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(harnessTimeout):
		t.Fatalf("timed out waiting for %s", description)
	}
}

// awaitCommandHandler waits for one Command handler's single result.
func awaitCommandHandler(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(harnessTimeout):
		t.Fatal("Command did not respond")
		return nil
	}
}

// awaitScriptedCommand reads the next client frame and requires its command
// name, so a script reads as a flat sequence.
func awaitScriptedCommand(session *scriptedSession, want string) (scriptedRequest, bool) {
	request, ok := session.awaitRequest()
	if !ok {
		return scriptedRequest{}, false
	}
	if request.command() != want {
		session.t.Errorf("request = %q, want %q", request.command(), want)
		return scriptedRequest{}, false
	}
	return request, true
}

// assertHealthyGeneration asserts that a generation never reported unhealthy
// and never reconnected.
func assertHealthyGeneration(t *testing.T, session *runtimeSession) {
	t.Helper()
	for _, report := range session.health {
		if report.Status == adapter.HealthUnhealthy {
			t.Fatalf("unhealthy report = %#v, want a healthy generation", report)
		}
	}
	if session.logs.has("dependency.retrying") {
		t.Fatal("the generation reconnected")
	}
}

// assertSingleAcceptance asserts that each responder produced exactly one
// accepted response.
func assertSingleAcceptance(t *testing.T, responders ...*fakeResponder) {
	t.Helper()
	for index, responder := range responders {
		accepted, rejected, total := responderCounts(responder)
		if accepted != 1 || rejected != 0 || total != 1 {
			t.Fatalf("responder %d = %d/%d/%d, want a single acceptance", index, accepted, rejected, total)
		}
	}
}

// scriptSlowPollAtDeadline answers one set and then holds its poll unanswered
// while the same generation serves a second Command.
func scriptSlowPollAtDeadline(
	session *scriptedSession,
	pollOnce, secondPollOnce *sync.Once,
	pollRead, secondPollRead chan struct{},
) {
	if !session.completeHandshake() {
		return
	}
	setRequest, ok := awaitScriptedCommand(session, commandSetValue)
	if !ok {
		return
	}
	session.replySuccess(setRequest.messageID(), setValueSuccessResult())
	pollRequest, ok := awaitScriptedCommand(session, commandPollValue)
	if !ok {
		return
	}
	pollOnce.Do(func() { close(pollRead) })
	_ = pollRequest

	secondSet, ok := awaitScriptedCommand(session, commandSetValue)
	if !ok {
		return
	}
	session.replySuccess(secondSet.messageID(), setValueSuccessResult())
	secondPoll, ok := awaitScriptedCommand(session, commandPollValue)
	if !ok {
		return
	}
	secondPollOnce.Do(func() { close(secondPollRead) })
	session.replySuccess(secondPoll.messageID(), map[string]any{"value": 40})
	session.waitForClose()
}

// This test protects the poll/Command-deadline boundary end to end and fails if
// a poll still outstanding at the absolute Command deadline closes the whole
// connection generation. Only a timed-out SetValue may do that.
func TestCommandSlowPollAtDeadlineKeepsTheGenerationHealthy(t *testing.T) {
	t.Parallel()
	var pollOnce, secondPollOnce sync.Once
	pollRead := make(chan struct{})
	secondPollRead := make(chan struct{})
	server := startScriptedServer(t, func(session *scriptedSession) {
		scriptSlowPollAtDeadline(session, &pollOnce, &secondPollOnce, pollRead, secondPollRead)
	})
	session, recorder, zwave := startScriptedAdapter(t, server)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	deadline := time.Now().Add(200 * time.Millisecond)
	responder := newFakeResponder(recorder, session)
	first := submitCommand(t, zwave, commandFixture(entityID, `{"value":40}`, deadline), responder)
	awaitSignal(t, pollRead, "the accepted Command to poll")
	if err := awaitCommandHandler(t, first); err != nil {
		t.Fatalf("accepted Command error = %v, want nil", err)
	}

	// Let the absolute deadline pass while that poll is still outstanding.
	time.Sleep(max(time.Until(deadline), 0) + 100*time.Millisecond)

	secondResponder := newFakeResponder(recorder, session)
	second := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		secondResponder,
	)
	if err := awaitCommandHandler(t, second); err != nil {
		t.Fatalf("second Command error = %v, want the generation to still serve Commands", err)
	}
	awaitSignal(t, secondPollRead, "the generation to keep polling")
	waitFor(t, "second linked evidence", func() bool { return len(session.recordedLinked()) == 1 })
	time.Sleep(testQuietPeriod)
	assertHealthyGeneration(t, session)
	assertSingleAcceptance(t, responder, secondResponder)
}

// scriptLatePollAnswerThenSecondCommand leaves the first accepted Command's poll
// unanswered past its deadline and answers that abandoned correlation late before
// serving a second Command on the same generation.
func scriptLatePollAnswerThenSecondCommand(session *scriptedSession) {
	if !session.completeHandshake() {
		return
	}
	firstSet, ok := awaitScriptedCommand(session, commandSetValue)
	if !ok {
		return
	}
	session.replySuccess(firstSet.messageID(), setValueSuccessResult())
	firstPoll, ok := awaitScriptedCommand(session, commandPollValue)
	if !ok {
		return
	}
	// The Command deadline passes while this poll is unanswered, and its result
	// arrives afterwards.
	time.Sleep(testRequestDeadline * 3)
	session.replySuccess(firstPoll.messageID(), map[string]any{"value": 15})

	secondSet, ok := awaitScriptedCommand(session, commandSetValue)
	if !ok {
		return
	}
	session.replySuccess(secondSet.messageID(), setValueSuccessResult())
	secondPoll, ok := awaitScriptedCommand(session, commandPollValue)
	if !ok {
		return
	}
	session.replySuccess(secondPoll.messageID(), map[string]any{"value": 40})
	session.waitForClose()
}

// This test protects the abandoned-poll boundary through the production client
// and the runtime together, and fails if a late answer to a poll abandoned at its
// Command deadline is published as linked evidence or ends the generation.
func TestCommandLateAnswerToAnAbandonedPollIsIgnored(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, scriptLatePollAnswerThenSecondCommand)
	session, recorder, zwave := startScriptedAdapter(t, server)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	firstResponder := newFakeResponder(recorder, session)
	first := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(150*time.Millisecond)),
		firstResponder,
	)
	if err := awaitCommandHandler(t, first); err != nil {
		t.Fatalf("first Command error = %v, want an acceptance", err)
	}
	secondResponder := newFakeResponder(recorder, session)
	second := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		secondResponder,
	)
	if err := awaitCommandHandler(t, second); err != nil {
		t.Fatalf("second Command error = %v, want the generation to keep serving", err)
	}
	waitFor(t, "the second linked evidence", func() bool { return len(session.recordedLinked()) == 1 })
	time.Sleep(testQuietPeriod)

	if got, want := linkedValues(session.recordedLinked()), []string{"40"}; !equalStrings(got, want) {
		t.Fatalf("linked values = %v, want only the answered poll %v", got, want)
	}
	assertHealthyGeneration(t, session)
	assertSingleAcceptance(t, firstResponder, secondResponder)
}

// scriptPollRejectionThenSecondCommand rejects the first poll and then serves a
// second Command on the same generation.
func scriptPollRejectionThenSecondCommand(session *scriptedSession) {
	if !session.completeHandshake() {
		return
	}
	setRequest, ok := awaitScriptedCommand(session, commandSetValue)
	if !ok {
		return
	}
	session.replySuccess(setRequest.messageID(), setValueSuccessResult())
	pollRequest, ok := awaitScriptedCommand(session, commandPollValue)
	if !ok {
		return
	}
	session.replyRejection(pollRequest.messageID(), "Node is not responding")

	secondSet, ok := awaitScriptedCommand(session, commandSetValue)
	if !ok {
		return
	}
	session.replySuccess(secondSet.messageID(), setValueSuccessResult())
	secondPoll, ok := awaitScriptedCommand(session, commandPollValue)
	if !ok {
		return
	}
	session.replySuccess(secondPoll.messageID(), map[string]any{"value": 40})
	session.waitForClose()
}

// This test protects the poll-rejection classification end to end and fails if a
// deterministic upstream poll rejection ends the whole generation instead of
// only the accepted attempt's linked evidence.
func TestCommandUpstreamPollRejectionKeepsTheGenerationHealthy(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, scriptPollRejectionThenSecondCommand)
	session, recorder, zwave := startScriptedAdapter(t, server)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	firstResponder := newFakeResponder(recorder, session)
	first := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		firstResponder,
	)
	if err := awaitCommandHandler(t, first); err != nil {
		t.Fatalf("first Command error: %v", err)
	}

	secondResponder := newFakeResponder(recorder, session)
	second := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		secondResponder,
	)
	if err := awaitCommandHandler(t, second); err != nil {
		t.Fatalf("second Command error: %v", err)
	}
	waitFor(t, "second linked evidence", func() bool { return len(session.recordedLinked()) == 1 })
	time.Sleep(testQuietPeriod)
	assertHealthyGeneration(t, session)
	assertSingleAcceptance(t, firstResponder, secondResponder)
}

// This test protects the keyed Event boundary at the runtime, and fails if a
// keyed value Event is projected as State or drives a Command poll hint by
// aliasing the unkeyed Value that shares its Command Class, endpoint, and
// property.
func TestCommandKeyedValueEventIsNotProjectedOrHinted(t *testing.T) {
	t.Parallel()
	recorder, session, connection, zwave := startCommandRuntime(t, dimmerNodeFixture(23, "Hallway Dimmer"))
	entityID := routeEntityID(testNodeID, "brightness")
	responder := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	waitFor(t, "the first linked poll", func() bool { return len(session.recordedLinked()) == 1 })
	pollsBefore := len(connection.recordedPollCalls())

	current := testValueID(commandClassMultilevelSwitch, 0, valuePropertyCurrentValue)
	connection.emit(keyedValueUpdatedEvent(current, 2, "40"))
	time.Sleep(testQuietPeriod)

	if got := len(connection.recordedPollCalls()); got != pollsBefore {
		t.Fatalf("polls = %d, want %d (a keyed Event must not drive a hint)", got, pollsBefore)
	}
	if got := len(session.recordedObservations()); got != 2 {
		t.Fatalf("Observations = %d, want 2 (a keyed Event is not State)", got)
	}
	if got := len(session.recordedLinked()); got != 1 {
		t.Fatalf("linked Observations = %d, want 1", got)
	}
}
