//go:build integration

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/agent"
)

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
