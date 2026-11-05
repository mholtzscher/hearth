package agent

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
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
		stored.Calls = append(
			stored.Calls,
			ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments},
		)
	}
	stored.ToolCallID = message.ToolCallID
	stored.ToolName = message.ToolName
	if message.Role == schema.User {
		stored.ViewContext = retainedViewContext([]byte(row.MessageJson))
	}
	if message.Role == schema.Tool && message.ToolName == "present_view" &&
		message.Extra[modeledToolFailureKey] != true {
		if artifact, err := dashboards.ReadArtifact([]byte(message.Content)); err == nil {
			stored.Artifacts = []dashboards.Artifact{artifact}
		}
	}
	return stored, nil
}

func historyMessageFromRow(row dbsqlc.ListRecentMessageJsonRow) (historyRow, error) {
	var message schema.Message
	if unmarshalErr := json.Unmarshal([]byte(row.MessageJson), &message); unmarshalErr != nil {
		return historyRow{}, fmt.Errorf("agent history: %w", unmarshalErr)
	}
	if message.Role == schema.User && message.Extra != nil {
		if raw := rawViewContext([]byte(row.MessageJson)); raw != nil {
			message.Extra[viewContextKey] = raw
		}
	}
	prepared := prepareModelMessage(&message)
	raw, err := json.Marshal(prepared)
	if err != nil {
		return historyRow{}, fmt.Errorf("agent history: %w", err)
	}
	return historyRow{role: row.Role, bytes: len(raw), message: prepared}, nil
}
