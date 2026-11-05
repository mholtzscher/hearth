package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
)

const conversationProviderKey = "hearth_model_provider"

// The first user row pins a provider even after later history leaves the model
// input budget. Read only its provider field, not unrelated legacy message data.
func (service *Service) checkConversationProvider(ctx context.Context, conversationID string) error {
	name, err := service.queries.GetConversationProvider(
		ctx,
		dbsqlc.GetConversationProviderParams{ConversationID: conversationID},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent conversation provider: %w", err)
	}
	provider := Provider(name)
	if provider != ProviderOpenAI && provider != ProviderOpenCodeGo {
		return errors.New("agent conversation has invalid provider metadata")
	}
	if provider != service.provider {
		return messageTextError{detail: "agent provider changed; start a new conversation"}
	}
	return nil
}
