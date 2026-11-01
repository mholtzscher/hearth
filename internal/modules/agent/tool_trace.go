package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

const failedToolOutcome = "error: agent tool call failed; effect outcome unknown. Do not automatically repeat this call. Inspect durable evidence before deciding what happened."

// turnTrace is scoped to one runner. Model hooks commit the original assistant
// batch before tools run; tool hooks commit results using ADK's call identity.
// Repaired model-input placeholders never pass through these write hooks.
type turnTrace struct {
	*adk.BaseChatModelAgentMiddleware

	service        *Service
	conversationID string
	emit           func(TurnEvent)
	mu             sync.Mutex
	failures       []error
	artifacts      map[string]struct{}
	emitMu         sync.Mutex
	tools          []string
	final          *schema.Message
}

func (trace *turnTrace) AfterModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	_ *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	message := state.Messages[len(state.Messages)-1]
	if err := trace.service.persistMessage(ctx, trace.conversationID, message); err != nil {
		return ctx, state, tracePersistError{err: err}
	}
	if len(message.ToolCalls) == 0 {
		trace.final = message
	}
	return ctx, state, nil
}

func (trace *turnTrace) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	call *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, arguments string, options ...tool.Option) (string, error) {
		// A different call's blocked callback must not prevent this call from
		// reaching its result commit. Join the started callback before emitting
		// this call's completion, including on persistence failure.
		started := make(chan struct{})
		go func() {
			defer close(started)
			trace.emitEvent(
				TurnEvent{Type: EventToolStarted, CallID: call.CallID, Name: call.Name, Arguments: arguments},
			)
		}()

		output, err := endpoint(ctx, arguments, options...)
		result := output
		if err != nil {
			trace.recordFailure(ctx, err)
			result = failedToolOutcome
		}
		message := &schema.Message{Role: schema.Tool, ToolCallID: call.CallID, ToolName: call.Name, Content: result}
		if persistErr := trace.service.persistMessage(ctx, trace.conversationID, message); persistErr != nil {
			failure := tracePersistError{err: persistErr}
			trace.recordFailure(ctx, failure)
			<-started
			return "", failure
		}
		<-started
		trace.emitEvent(
			TurnEvent{
				Type:   EventToolFinished,
				CallID: call.CallID,
				Name:   call.Name,
				Result: truncateEventResult(result),
			},
		)
		if err == nil && call.Name == "present_view" {
			if artifact, parseErr := dashboards.ParseArtifact([]byte(output)); parseErr == nil {
				trace.mu.Lock()
				if trace.artifacts == nil {
					trace.artifacts = make(map[string]struct{})
				}
				trace.artifacts[artifact.ArtifactID] = struct{}{}
				trace.mu.Unlock()
				trace.emitEvent(TurnEvent{Type: EventViewPresented, CallID: call.CallID, Artifact: &artifact})
			}
		}
		return output, err
	}, nil
}

// Slow clients serialize callbacks, never persistence of another tool's result.
func (trace *turnTrace) emitEvent(event TurnEvent) {
	trace.emitMu.Lock()
	defer trace.emitMu.Unlock()
	if event.Type == EventToolStarted {
		trace.tools = append(trace.tools, event.Name)
	}
	if trace.emit != nil {
		trace.emit(event)
	}
}

func (trace *turnTrace) recordFailure(ctx context.Context, err error) {
	// Several canceled hooks reflect the same owner cancellation, not separate
	// faults. The runner already carries that cancellation to the turn boundary.
	if expectedTurnCancellation(ctx, err) {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.failures = append(trace.failures, err)
}

// ADK returns the first parallel tool error in call order. After the iterator
// joins, retain other failures too so persistence faults cannot be hidden by a
// sibling transport fault or cancellation. Avoid joining an already wrapped
// cause: a single owner cancellation must keep its single-cause identity.
func (trace *turnTrace) failure(runErr error) error {
	for _, err := range trace.failures {
		if errors.Is(runErr, err) {
			continue
		}
		if runErr == nil {
			runErr = err
		} else {
			runErr = errors.Join(runErr, err)
		}
	}
	return runErr
}
