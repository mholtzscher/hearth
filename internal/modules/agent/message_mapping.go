package agent

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
)

func storedMessageFromRow(row dbsqlc.ListMessagesRow) (StoredMessage, error) {
	var message schema.Message
	if unmarshalErr := json.Unmarshal([]byte(row.MessageJson), &message); unmarshalErr != nil {
		return StoredMessage{}, fmt.Errorf("agent history: %w", unmarshalErr)
	}
	parsed, parseErr := decodeAgentTimestamp(row.CreatedAt)
	if parseErr != nil {
		return StoredMessage{}, fmt.Errorf("agent history: %w", parseErr)
	}
	stored := StoredMessage{
		ID:        row.ID,
		Role:      row.Role,
		Content:   message.Content,
		CreatedAt: parsed,
	}
	for _, call := range message.ToolCalls {
		stored.ToolCalls = append(stored.ToolCalls, call.Function.Name)
	}
	return stored, nil
}

func historyMessageFromRow(row dbsqlc.ListRecentMessageJsonRow) (historyRow, error) {
	var message schema.Message
	if unmarshalErr := json.Unmarshal([]byte(row.MessageJson), &message); unmarshalErr != nil {
		return historyRow{}, fmt.Errorf("agent history: %w", unmarshalErr)
	}
	return historyRow{role: row.Role, bytes: len(row.MessageJson), message: &message}, nil
}
