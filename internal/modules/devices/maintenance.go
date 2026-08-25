package devices

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	commandsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/commands"
	receiptsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/receipts"
)

func recoverAndPrune(ctx context.Context, database *sql.DB, now time.Time) error {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin Device / Entity recovery: %w", err)
	}
	defer tx.Rollback()

	if _, err := commandsqlc.New(tx).InterruptActiveCommands(ctx, commandsqlc.InterruptActiveCommandsParams{
		CompletedAt: sql.NullString{String: formatTime(now), Valid: true},
	}); err != nil {
		return fmt.Errorf("interrupt active commands: %w", err)
	}
	if err := pruneExpiredObservationReceipts(ctx, tx, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Device / Entity recovery: %w", err)
	}
	return nil
}

func pruneExpiredObservationReceipts(ctx context.Context, database sqliteDBTX, before time.Time) error {
	if _, err := receiptsqlc.New(database).DeleteExpiredObservationReceipts(ctx, receiptsqlc.DeleteExpiredObservationReceiptsParams{
		ExpiresAt: formatTime(before),
	}); err != nil {
		return fmt.Errorf("delete expired observation receipts: %w", err)
	}
	return nil
}
