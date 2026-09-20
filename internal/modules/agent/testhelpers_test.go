package agent //nolint:testpackage // Tests construct the service with an injected fake model.

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// fakeChatModel is a scripted ToolCallingChatModel. Generate signals started,
// then waits for release or context cancellation, so a test can hold one turn
// open and drive Drain deterministically.
type fakeChatModel struct {
	started chan struct{}
	release chan struct{}
	reply   string
	once    sync.Once
}

func newFakeChatModel(reply string) *fakeChatModel {
	return &fakeChatModel{
		started: make(chan struct{}),
		release: make(chan struct{}),
		reply:   reply,
	}
}

func (fake *fakeChatModel) Generate(
	ctx context.Context,
	_ []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	fake.once.Do(func() { close(fake.started) })
	select {
	case <-fake.release:
		return schema.AssistantMessage(fake.reply, nil), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (fake *fakeChatModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("fakeChatModel: Stream is not supported")
}

func (fake *fakeChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return fake, nil
}

// stubTool is a Tool.BaseTool that is never invoked: the scripted model returns
// a final answer without emitting tool calls.
type stubTool struct{ name string }

func (stub stubTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: stub.name, Desc: "test-only tool"}, nil
}

func (stub stubTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", nil
}

// newTurnService builds a real service over real SQLite with an injected model.
func newTurnService(t *testing.T, chatModel model.ToolCallingChatModel) *Service {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, err := NewService(context.Background(), Config{
		DB:        database,
		Tools:     []tool.BaseTool{stubTool{name: "noop"}},
		ChatModel: chatModel,
		Retention: MinimumConversationRetention,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// newConversationStore builds the database-only service the retention tests
// need: PruneHistory touches no chat model.
func newConversationStore(t *testing.T, retention time.Duration) *Service {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	return &Service{database: database, queries: dbsqlc.New(database), retention: retention}
}

// mustCreateConversation opens one conversation through the real path.
func mustCreateConversation(t *testing.T, service *Service) Conversation {
	t.Helper()
	conversation, err := service.CreateConversation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return conversation
}
