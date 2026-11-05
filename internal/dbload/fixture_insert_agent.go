package dbload

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
)

// insertAgent seeds conversations and valid Eino message JSON for each.
func insertAgent(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		conversationInsert, err := prepareStatement(ctx, tx, `
INSERT INTO agent_conversations (id, created_at) VALUES (?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = conversationInsert.Close() }()
		messageInsert, err := prepareStatement(ctx, tx, `
INSERT INTO agent_messages (conversation_id, role, message_json, created_at) VALUES (?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = messageInsert.Close() }()
		for conversation := range plan.conversations {
			if err = insertConversation(
				ctx, plan, conversation, conversationInsert, messageInsert,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertConversation(
	ctx context.Context,
	plan fixturePlan,
	conversation int,
	conversationInsert, messageInsert *sql.Stmt,
) error {
	conversationID := fixtureID("aconv_", fixtureIndex(conversation))
	createdOffset := -time.Duration(conversation+1) * time.Hour
	if _, err := conversationInsert.ExecContext(
		ctx, conversationID, fixtureStamp(createdOffset),
	); err != nil {
		return fmt.Errorf("insert agent conversation: %w", err)
	}
	for message := range plan.messagesPerConversation {
		role := schema.User
		if message%2 == 1 {
			role = schema.Assistant
		}
		body, err := fixtureMessageJSON(
			role,
			fmt.Sprintf("Fixture message %d in conversation %d", message, conversation),
		)
		if err != nil {
			return err
		}
		if _, err = messageInsert.ExecContext(
			ctx, conversationID, string(role), string(body),
			fixtureStamp(createdOffset+time.Duration(message)*time.Minute),
		); err != nil {
			return fmt.Errorf("insert agent message: %w", err)
		}
	}
	return nil
}
