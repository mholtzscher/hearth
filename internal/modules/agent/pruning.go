package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
)

// MinimumConversationRetention is the shortest whole-conversation retention
// PruneHistory will prune with. It matches the application's configured floor,
// and a shorter or unconfigured retention fails rather than deleting more
// history than intended.
const MinimumConversationRetention = 24 * time.Hour

// conversationPruneBatch bounds one retention statement.
const conversationPruneBatch = 50

// PruneHistory deletes whole conversations with no activity at or after
// now minus the configured Retention. Activity is the newest persisted message,
// falling back to creation time for an untouched conversation; messages cascade
// with the conversation they belong to. Deletion repeats in
// conversationPruneBatch-sized statements until one deletes fewer rows.
func (service *Service) PruneHistory(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return errors.New("agent history prune time is required")
	}
	if service.retention < MinimumConversationRetention {
		return fmt.Errorf(
			"agent retention %s is below the minimum %s",
			service.retention, MinimumConversationRetention,
		)
	}
	cutoff := encodeAgentTimestamp(now.UTC().Add(-service.retention))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deleted, err := service.deleteConversationsBefore(ctx, cutoff, conversationPruneBatch)
		if err != nil {
			return err
		}
		if deleted < conversationPruneBatch {
			return nil
		}
	}
}

// deleteConversationsBefore deletes up to batch conversations whose creation
// time and every message predate cutoff, returning how many rows the statement
// removed. It is one statement so candidate selection and deletion are atomic.
func (service *Service) deleteConversationsBefore(ctx context.Context, cutoff string, batch int) (int64, error) {
	deleted, err := service.queries.DeleteConversationsBefore(ctx, dbsqlc.DeleteConversationsBeforeParams{
		Cutoff:    cutoff,
		BatchSize: int64(batch),
	})
	if err != nil {
		return 0, fmt.Errorf("agent prune conversations: %w", err)
	}
	return deleted, nil
}
