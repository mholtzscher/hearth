package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdmissionOpenUntilStopped(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, newFakeChatModel("unused"))
	if !service.AdmissionOpen() {
		t.Fatal("admission is closed before StopAdmission")
	}
	service.StopAdmission()
	if service.AdmissionOpen() {
		t.Fatal("admission is open after StopAdmission")
	}
	// Idempotent.
	service.StopAdmission()
}

func TestStopAdmissionRejectsNewTurns(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, newFakeChatModel("unused"))
	conversation := mustCreateConversation(t, service)
	service.StopAdmission()

	_, err := service.SendMessage(context.Background(), conversation.ID, "hello")
	if !errors.Is(err, ErrAdmissionUnavailable) {
		t.Fatalf("SendMessage error = %v, want %v", err, ErrAdmissionUnavailable)
	}
	// Reads and conversation creation are not turn admission and stay available.
	history, historyErr := service.History(context.Background(), conversation.ID)
	if historyErr != nil {
		t.Fatalf("History after StopAdmission: %v", historyErr)
	}
	if len(history) != 0 {
		t.Fatalf("history = %d rows, want 0", len(history))
	}
}

func TestSendMessageWithEventsClosesStreamOnClosedAdmission(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, newFakeChatModel("unused"))
	conversation := mustCreateConversation(t, service)
	service.StopAdmission()

	var events []TurnEvent
	_, err := service.SendMessageWithEvents(
		context.Background(), conversation.ID, "hello",
		func(event TurnEvent) { events = append(events, event) },
	)
	if !errors.Is(err, ErrAdmissionUnavailable) {
		t.Fatalf("error = %v, want admission unavailable", err)
	}
	if len(events) != 1 || events[0].Type != EventTurnFailed {
		t.Fatalf("events = %+v, want one terminal turn.failed", events)
	}
	if events[0].Error != "agent admission is unavailable" {
		t.Fatalf("event error = %q, want the admission detail", events[0].Error)
	}
}

func TestSendMessagePersistsTurnWithInjectedModel(t *testing.T) {
	t.Parallel()
	chatModel := newFakeChatModel("all good")
	close(chatModel.release)
	service := newTurnService(t, chatModel)
	conversation := mustCreateConversation(t, service)

	turn, err := service.SendMessage(context.Background(), conversation.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if turn.Reply != "all good" {
		t.Fatalf("reply = %q, want injected model reply", turn.Reply)
	}
	history, err := service.History(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %d rows, want user plus assistant", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Fatalf("first row = %+v, want the user message", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "all good" {
		t.Fatalf("second row = %+v, want the assistant reply", history[1])
	}
}

func TestDrainCancelsActiveTurnAndJoins(t *testing.T) {
	t.Parallel()
	chatModel := newFakeChatModel("unused")
	service := newTurnService(t, chatModel)
	conversation := mustCreateConversation(t, service)
	ctx := context.Background()

	turnDone := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, conversation.ID, "hello")
		turnDone <- err
	}()
	select {
	case <-chatModel.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never reached the injected model")
	}

	drainDone := make(chan error, 1)
	go func() { drainDone <- service.Drain(ctx) }()
	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not join the cancelled turn")
	}

	select {
	case err := <-turnDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled turn error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn did not return")
	}

	if service.AdmissionOpen() {
		t.Fatal("Drain left admission open")
	}
	if _, err := service.SendMessage(ctx, conversation.ID, "again"); !errors.Is(err, ErrAdmissionUnavailable) {
		t.Fatalf("post-Drain SendMessage error = %v, want admission unavailable", err)
	}
	// The persisted turn stops at the user message: cancellation never wrote a
	// half-finished assistant or tool trace.
	history, err := service.History(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Role != "user" {
		t.Fatalf("history after cancellation = %+v, want only the user message", history)
	}
}

func TestDrainJoinsWithoutActiveTurns(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, newFakeChatModel("unused"))
	if err := service.Drain(context.Background()); err != nil {
		t.Fatalf("Drain with no active turns: %v", err)
	}
	if service.AdmissionOpen() {
		t.Fatal("Drain left admission open")
	}
}
