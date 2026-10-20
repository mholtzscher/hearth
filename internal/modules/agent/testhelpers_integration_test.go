//go:build integration

package agent //nolint:testpackage // Tests construct the service with an injected fake model.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

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
