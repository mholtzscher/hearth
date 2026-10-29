package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
)

// maxPreviewRunes bounds the sidebar preview taken from the first user message.
const maxPreviewRunes = 80

func conversationSummaryFromRow(row dbsqlc.ListConversationSummariesRow) (ConversationSummary, error) {
	parsed, parseErr := decodeAgentTimestamp(row.CreatedAt)
	if parseErr != nil {
		return ConversationSummary{}, fmt.Errorf("agent list conversations: %w", parseErr)
	}
	summary := ConversationSummary{
		ID:           row.ID,
		CreatedAt:    parsed,
		MessageCount: int(row.MessageCount),
	}
	if row.LastMessageAt.Valid {
		last, lastErr := decodeAgentTimestamp(row.LastMessageAt.String)
		if lastErr != nil {
			return ConversationSummary{}, fmt.Errorf("agent list conversations: %w", lastErr)
		}
		summary.LastMessageAt = &last
	}
	if row.FirstUserMessageJson.Valid {
		preview, previewErr := previewFromMessageJSON(row.FirstUserMessageJson.String)
		if previewErr != nil {
			return ConversationSummary{}, previewErr
		}
		summary.Preview = preview
	}
	return summary, nil
}

// previewFromMessageJSON renders the sidebar preview from a persisted message
// row: its content with whitespace collapsed and bounded by maxPreviewRunes.
func previewFromMessageJSON(raw string) (string, error) {
	var message schema.Message
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		return "", fmt.Errorf("agent list conversations: %w", err)
	}
	preview := strings.Join(strings.Fields(message.Content), " ")
	if len([]rune(preview)) > maxPreviewRunes {
		preview = string([]rune(preview)[:maxPreviewRunes]) + "…"
	}
	return preview, nil
}
