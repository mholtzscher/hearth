package agent

import (
	"context"
	"fmt"
	"log/slog"
)

// Conversation operations and stream preflight do not enter the turn failure
// boundary. Diagnose them here; turn execution keeps its service-owned record.
func logHTTPFailure(ctx context.Context, logger *slog.Logger, operation, stage string, err error) {
	if expectedTurnCancellation(ctx, err) {
		return
	}
	logger.ErrorContext(ctx, "agent HTTP operation failed",
		slog.String("event", "agent.http_internal_failure"),
		slog.String("operation", operation), slog.String("stage", stage),
		slog.String("error_code", "internal_error"), slog.String("error_type", fmt.Sprintf("%T", err)),
	)
}
