package simulator

import (
	"context"
	"log/slog"
)

// Run validates config and then runs the scripted simulator process until ctx
// ends. Scripted Devices are the only simulation mode: every declared Device is
// registered and driven from YAML by the scripted runtime.
func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	return runScripted(ctx, config, logger)
}
