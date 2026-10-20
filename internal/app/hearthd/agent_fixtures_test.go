package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/agent"
)

// testAgentAPIKeyFile writes a throwaway model API key and returns its path, so
// the required agent can start without a live provider.
func testAgentAPIKeyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-api-key")
	if err := os.WriteFile(path, []byte("test-model-api-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// requiredAgentConfig returns the minimal agent configuration Core requires to
// start: the secret file path. The agent is a required module, so every Run
// test needs one.
func requiredAgentConfig(t *testing.T) AgentConfig {
	t.Helper()
	return AgentConfig{APIKeyFile: testAgentAPIKeyFile(t)}
}

// stubAgent is the narrow agent Operations seam the runtime HTTP handler is
// assembled with. Every unset hook panics, so a test cannot silently exercise
// an operation it did not intend.
type stubAgent struct {
	admissionClosed       bool
	createConversation    func(context.Context) (agent.Conversation, error)
	listConversations     func(context.Context) ([]agent.ConversationSummary, error)
	sendMessage           func(context.Context, string, string) (agent.Turn, error)
	sendMessageWithEvents func(context.Context, string, string, func(agent.TurnEvent)) (agent.Turn, error)
	history               func(context.Context, string) ([]agent.StoredMessage, error)
	conversationExists    func(context.Context, string) (bool, error)
}

func (stub *stubAgent) AdmissionOpen() bool { return !stub.admissionClosed }

func (stub *stubAgent) CreateConversation(ctx context.Context) (agent.Conversation, error) {
	if stub.createConversation == nil {
		panic("unexpected CreateConversation call")
	}
	return stub.createConversation(ctx)
}

func (stub *stubAgent) ListConversations(ctx context.Context) ([]agent.ConversationSummary, error) {
	if stub.listConversations == nil {
		panic("unexpected ListConversations call")
	}
	return stub.listConversations(ctx)
}

func (stub *stubAgent) SendMessage(ctx context.Context, conversationID, text string) (agent.Turn, error) {
	if stub.sendMessage == nil {
		panic("unexpected SendMessage call")
	}
	return stub.sendMessage(ctx, conversationID, text)
}

func (stub *stubAgent) SendMessageWithEvents(
	ctx context.Context,
	conversationID, text string,
	emit func(agent.TurnEvent),
) (agent.Turn, error) {
	if stub.sendMessageWithEvents == nil {
		panic("unexpected SendMessageWithEvents call")
	}
	return stub.sendMessageWithEvents(ctx, conversationID, text, emit)
}

func (stub *stubAgent) History(ctx context.Context, conversationID string) ([]agent.StoredMessage, error) {
	if stub.history == nil {
		panic("unexpected History call")
	}
	return stub.history(ctx, conversationID)
}

func (stub *stubAgent) ConversationExists(ctx context.Context, conversationID string) (bool, error) {
	if stub.conversationExists == nil {
		panic("unexpected ConversationExists call")
	}
	return stub.conversationExists(ctx, conversationID)
}
