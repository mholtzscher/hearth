package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

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

// stubAgentChatModel is the Config.Agent.ChatModel test seam: app tests build
// the real agent service against a scripted model instead of a provider. When
// started and release are set it holds one turn open, signaling started on
// entry and canceled when Drain cancels the turn, so a test can observe
// shutdown ordering.
type stubAgentChatModel struct {
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
	once     sync.Once
	cancel   sync.Once
}

func newBlockingAgentChatModel() *stubAgentChatModel {
	return &stubAgentChatModel{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
}

func (stub *stubAgentChatModel) Generate(
	ctx context.Context,
	_ []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	if stub.started == nil {
		return schema.AssistantMessage("ok", nil), nil
	}
	stub.once.Do(func() { close(stub.started) })
	select {
	case <-stub.release:
	case <-ctx.Done():
		stub.cancel.Do(func() { close(stub.canceled) })
		// Keep the turn open so the test can prove shutdown has not withdrawn
		// dependencies or closed SQLite while an admitted turn is still running.
		<-stub.release
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return schema.AssistantMessage("ok", nil), nil
}

func (*stubAgentChatModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stubAgentChatModel: Stream is not supported")
}

func (stub *stubAgentChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return stub, nil
}

// stubAgentTool is a Tool.BaseTool that satisfies construction; the scripted
// model returns a final answer without calling it.
type stubAgentTool struct{}

func (stubAgentTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "test_noop", Desc: "test-only tool"}, nil
}

func (stubAgentTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", nil
}

// newAgentServiceForTest builds the real agent service over the given database
// with the chat model seam, so app tests exercise production assembly without
// model credentials or a network call.
func newAgentServiceForTest(
	t *testing.T,
	database *sql.DB,
	chatModel model.ToolCallingChatModel,
	retention time.Duration,
) *agent.Service {
	t.Helper()
	service, err := agent.NewService(context.Background(), agent.Config{
		DB:        database,
		Tools:     []tool.BaseTool{stubAgentTool{}},
		ChatModel: chatModel,
		Retention: retention,
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
