package agent //nolint:testpackage // Tests exercise package-private turn state bookkeeping.

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// This test fails if a conversation reports no running turn while another turn
// waits for it, or if a running state outlives every turn in the conversation.
// It replays the race where the waiting turn gives up after the running turn
// saw it waiting but before that turn released the conversation.
func TestTurnStateFollowsTheConversationGate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		service := &Service{}
		const id = "aconv_gate"
		service.setTurnState(id, TurnState{State: TurnStateRunning, StartedAt: time.Now()})
		release, err := service.acquireConversationTurn(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if service.turnWaiting(id) {
			t.Fatal("a turn holding the slot alone reports a waiter")
		}
		waiting, giveUp := context.WithCancel(t.Context())
		gaveUp := make(chan error)
		go func() {
			_, waitErr := service.acquireConversationTurn(waiting, id)
			gaveUp <- waitErr
		}()
		synctest.Wait()
		if !service.turnWaiting(id) {
			t.Fatal("a queued turn is not reported as waiting")
		}
		// The running turn finished and kept its state for the waiter, which
		// now gives up while the running turn still holds the conversation.
		giveUp()
		if waitErr := <-gaveUp; waitErr == nil {
			t.Fatal("the waiting turn acquired the held conversation")
		}
		if _, ok := service.TurnState(id); !ok {
			t.Fatal("running state cleared while a turn still holds the conversation")
		}
		release()
		if state, ok := service.TurnState(id); ok {
			t.Fatalf("running state %+v outlived every turn", state)
		}

		failed := TurnState{State: TurnStateFailed, Error: "agent turn failed", Prompt: "Chart the garage"}
		service.setTurnState(id, failed)
		release, err = service.acquireConversationTurn(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		release()
		if state, _ := service.TurnState(id); state != failed {
			t.Fatalf("releasing the conversation removed a failed state: %+v", state)
		}
	})
}
