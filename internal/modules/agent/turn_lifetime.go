package agent

import (
	"context"
	"sync"
	"time"
)

// DefaultTurnTimeout bounds one turn from admission to its final reply when
// Config.TurnTimeout is zero. It covers the model-generation cycles and tool
// calls of a long dashboard or Automation authoring turn.
const DefaultTurnTimeout = 5 * time.Minute

// Turn states a conversation reports while its latest turn runs or after it
// failed. A conversation whose latest turn finished, or that has had none since
// Core started, reports no state.
const (
	TurnStateRunning = "running"
	TurnStateFailed  = "failed"
)

// TurnState is the in-memory state of a conversation's latest turn. It is lost
// when Core restarts. Error and Prompt are set only for a failed turn: the
// public failure text and the turn's own message, which the turn may have
// failed before saving.
type TurnState struct {
	State     string
	StartedAt time.Time
	Error     string
	Prompt    string
}

// turnDeadlineError marks a turn that outlived its own deadline. It is a
// failure, not a cancellation: no owner stopped the turn.
type turnDeadlineError struct{ err error }

func (deadline turnDeadlineError) Error() string {
	return "agent turn timed out: " + deadline.err.Error()
}

func (deadline turnDeadlineError) Unwrap() error { return deadline.err }

// TurnContinuesError reports that the caller stopped waiting while its turn
// keeps running. The turn's outcome is in the conversation's history and
// TurnState.
type TurnContinuesError struct{ err error }

func (continues TurnContinuesError) Error() string {
	return "agent turn continues without its caller: " + continues.err.Error()
}

func (continues TurnContinuesError) Unwrap() error { return continues.err }

// turnListener forwards a turn's events to the caller that started it until
// that caller leaves. Detach waits for an event being delivered, so a transport
// never receives an event after it stopped listening.
type turnListener struct {
	mutex sync.Mutex
	emit  func(TurnEvent)
}

func newTurnListener(emit func(TurnEvent)) *turnListener {
	return &turnListener{emit: emit}
}

func (listener *turnListener) send(event TurnEvent) {
	listener.mutex.Lock()
	defer listener.mutex.Unlock()
	if listener.emit != nil {
		listener.emit(event)
	}
}

func (listener *turnListener) detach() {
	listener.mutex.Lock()
	defer listener.mutex.Unlock()
	listener.emit = nil
}

// TurnState reports the latest turn's state for a conversation, or false when
// it finished or none has run since Core started.
func (service *Service) TurnState(conversationID string) (TurnState, bool) {
	service.turnStatesMu.Lock()
	defer service.turnStatesMu.Unlock()
	state, ok := service.turnStates[conversationID]
	return state, ok
}

func (service *Service) setTurnState(conversationID string, state TurnState) {
	service.turnStatesMu.Lock()
	defer service.turnStatesMu.Unlock()
	if service.turnStates == nil {
		service.turnStates = make(map[string]TurnState)
	}
	service.turnStates[conversationID] = state
}

// clearRunningState removes a running state and keeps a failed one. Callers
// hold the gate lock.
func (service *Service) clearRunningState(conversationID string) {
	service.turnStatesMu.Lock()
	defer service.turnStatesMu.Unlock()
	if service.turnStates[conversationID].State == TurnStateRunning {
		delete(service.turnStates, conversationID)
	}
}

func (service *Service) clearTurnState(conversationID string) {
	service.turnStatesMu.Lock()
	defer service.turnStatesMu.Unlock()
	delete(service.turnStates, conversationID)
}

// turnContext gives a turn its own lifetime: it keeps the caller's values but
// not its cancellation, and ends at the turn deadline or when Drain cancels it.
func (service *Service) turnContext(caller context.Context) (context.Context, context.CancelFunc) {
	timeout := service.turnTimeout
	if timeout <= 0 {
		timeout = DefaultTurnTimeout
	}
	return context.WithTimeout(context.WithoutCancel(caller), timeout)
}
