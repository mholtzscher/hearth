//go:build integration

package zwavejs //nolint:testpackage // Command tests exercise the private coordinator and connection seam.

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

// startScriptedAdapter runs one Adapter over the production WebSocket client against a scripted server, so a regression
// test crosses the real client correlation rule instead of only the fake connection seam.
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

// awaitScriptedCommand reads the next client frame and requires its command name, so a script reads as a flat sequence.
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

// scriptSlowPollAtDeadline answers one set and then holds its poll unanswered while the same generation serves a second
// Command.
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

// This test protects the poll/Command-deadline boundary end to end and fails if a poll still outstanding at the
// absolute Command deadline closes the whole connection generation. Only a timed-out SetValue may do that.
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

// scriptLatePollAnswerThenSecondCommand leaves the first accepted Command's poll unanswered past its deadline and
// answers that abandoned correlation late before serving a second Command on the same generation.
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
	// The Command deadline passes while this poll is unanswered, and its result arrives afterwards.
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

// This test protects the abandoned-poll boundary through the production client and the runtime together, and fails if a
// late answer to a poll abandoned at its Command deadline is published as linked evidence or ends the generation.
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

// scriptPollRejectionThenSecondCommand rejects the first poll and then serves a second Command on the same generation.
func scriptPollRejectionThenSecondCommand(session *scriptedSession) {
	if !session.completeHandshake() {
		return
	}
	setRequest, ok := awaitScriptedCommand(session, commandSetValue)
	if !ok {
		return
	}
	session.replySuccess(setRequest.messageID(), map[string]any{
		"result": map[string]any{"status": setValueStatusWorking},
	})
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

// A rejected poll is not evidence that a Working write finished. The next write must wait for the first attempt's
// deadline without losing the connection.
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
		commandFixture(entityID, `{"value":40}`, time.Now().Add(350*time.Millisecond)),
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
	select {
	case err := <-second:
		t.Fatalf("second Command advanced past rejected poll before deadline: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := awaitCommandHandler(t, second); err != nil {
		t.Fatalf("second Command error: %v", err)
	}
	waitFor(t, "second linked evidence", func() bool { return len(session.recordedLinked()) == 1 })
	time.Sleep(testQuietPeriod)
	assertHealthyGeneration(t, session)
	assertSingleAcceptance(t, firstResponder, secondResponder)
}

// scriptSetValueRejectionThenSecondCommand rejects the first node.set_value and then serves a second Command on the
// same generation.
func scriptSetValueRejectionThenSecondCommand(session *scriptedSession) {
	if !session.completeHandshake() {
		return
	}
	firstSet, ok := awaitScriptedCommand(session, commandSetValue)
	if !ok {
		return
	}
	session.replyRejection(firstSet.messageID(), "The node did not respond")

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

// This test protects the SetValue classification end to end and fails if a deterministic upstream SetValue
// result-envelope rejection ends the whole generation, or rejects the Command as an unavailable Entity, instead of
// rejecting only the Command.
func TestCommandUpstreamSetValueRejectionKeepsTheGenerationHealthy(t *testing.T) {
	t.Parallel()
	server := startScriptedServer(t, scriptSetValueRejectionThenSecondCommand)
	session, recorder, zwave := startScriptedAdapter(t, server)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "brightness")
	rejectedResponder := &recordingRejectionResponder{}
	rejected := submitCommand(
		t,
		zwave,
		commandFixture(entityID, `{"value":40}`, time.Now().Add(time.Minute)),
		rejectedResponder,
	)
	if err := awaitCommandHandler(t, rejected); err != nil {
		t.Fatalf("rejected Command error = %v, want a plain rejection", err)
	}
	if rejectedResponder.rejected != 1 || rejectedResponder.unavailable != 0 {
		t.Fatalf(
			"rejected Command responses = rejected %d unavailable %d, want a deterministic rejection",
			rejectedResponder.rejected, rejectedResponder.unavailable,
		)
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
	waitFor(t, "second linked evidence", func() bool { return len(session.recordedLinked()) == 1 })
	time.Sleep(testQuietPeriod)
	assertHealthyGeneration(t, session)
	accepted, rejectedCount, total := responderCounts(secondResponder)
	if accepted != 1 || rejectedCount != 0 || total != 1 {
		t.Fatalf("second Command responses = %d/%d/%d, want a single acceptance", accepted, rejectedCount, total)
	}
}
