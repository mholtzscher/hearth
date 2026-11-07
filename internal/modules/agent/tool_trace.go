package agent

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

const failedToolOutcome = "error: agent tool call failed; effect outcome unknown. Do not automatically repeat this call. Inspect durable evidence before deciding what happened."

const modeledToolFailureKey = "hearth_mcp_modeled_failure"

// The MCP bridge records modeled failures without changing their model-visible
// output. The status is scoped to this invocation and never exposed publicly.
type toolResultStatusKey struct{}
type toolResultStatus struct{ modeledFailure bool }

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
	artifacts      []committedArtifact
	emitMu         sync.Mutex
	tools          []string
	final          *schema.Message
}

type committedArtifact struct {
	rowID    int64
	artifact Artifact
}

func (trace *turnTrace) hasArtifacts() bool {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return len(trace.artifacts) > 0
}

// committedArtifacts is called only after the runner joins all tool hooks.
func (trace *turnTrace) committedArtifacts() []Artifact {
	slices.SortFunc(trace.artifacts, func(a, b committedArtifact) int { return cmp.Compare(a.rowID, b.rowID) })
	var artifacts []Artifact
	for _, item := range trace.artifacts {
		artifacts = append(artifacts, item.artifact)
	}
	return artifacts
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

		status := &toolResultStatus{}
		output, err := endpoint(context.WithValue(ctx, toolResultStatusKey{}, status), arguments, options...)
		result := output
		if err != nil {
			trace.recordFailure(ctx, err)
			result = failedToolOutcome
		}
		message := &schema.Message{Role: schema.Tool, ToolCallID: call.CallID, ToolName: call.Name, Content: result}
		if status.modeledFailure {
			message.Extra = map[string]any{modeledToolFailureKey: true}
		}
		rowID, persistErr := trace.service.persistMessageWithID(ctx, trace.conversationID, message)
		if persistErr != nil {
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
		if err == nil && !status.modeledFailure && call.Name == "present_view" {
			if artifact, parseErr := dashboards.ParseArtifact([]byte(output)); parseErr == nil {
				projected := viewArtifact(artifact)
				trace.mu.Lock()
				trace.artifacts = append(trace.artifacts, committedArtifact{rowID: rowID, artifact: projected})
				trace.mu.Unlock()
				trace.emitEvent(TurnEvent{Type: EventViewPresented, CallID: call.CallID, Artifact: &projected})
			}
		}
		if err == nil && !status.modeledFailure && call.Name == "present_automation" {
			if artifact, parseErr := automations.ParseArtifact([]byte(output)); parseErr == nil {
				projected := automationArtifact(artifact)
				trace.mu.Lock()
				trace.artifacts = append(trace.artifacts, committedArtifact{rowID: rowID, artifact: projected})
				trace.mu.Unlock()
				trace.emitEvent(TurnEvent{Type: EventAutomationPresented, CallID: call.CallID, Artifact: &projected})
			}
		}
		return output, err
	}, nil
}

// toolPreparingKey carries the turn's callback for tool calls the model has
// begun writing, so the model adapter can announce them before it returns.
type toolPreparingKey struct{}

func withToolPreparing(ctx context.Context, announce func(callID, name string)) context.Context {
	return context.WithValue(ctx, toolPreparingKey{}, announce)
}

func announceToolPreparing(ctx context.Context, callID, name string) {
	if announce, ok := ctx.Value(toolPreparingKey{}).(func(callID, name string)); ok && callID != "" && name != "" {
		announce(callID, name)
	}
}

func (trace *turnTrace) preparing(callID, name string) {
	trace.emitEvent(TurnEvent{Type: EventToolPreparing, CallID: callID, Name: name})
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
