package devices

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	commandsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/commands"
	receiptsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/receipts"
)

func interruptActiveCommands(ctx context.Context, database sqliteDBTX, completedAt time.Time) error {
	_, err := commandsqlc.New(database).InterruptActiveCommands(ctx, commandsqlc.InterruptActiveCommandsParams{
		CompletedAt: sql.NullString{String: formatTime(completedAt), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("interrupt active commands: %w", err)
	}
	return nil
}

func deleteExpiredObservationReceipts(ctx context.Context, database sqliteDBTX, before time.Time) error {
	_, err := receiptsqlc.New(database).DeleteExpiredObservationReceipts(ctx, receiptsqlc.DeleteExpiredObservationReceiptsParams{
		ExpiresAt: formatTime(before),
	})
	if err != nil {
		return fmt.Errorf("delete expired observation receipts: %w", err)
	}
	return nil
}
