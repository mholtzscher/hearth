package automations_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func terminalCommand(
	t *testing.T,
	input devices.CommandInput,
	status devices.CommandStatus,
	failureCode *devices.CommandFailureCode,
) devices.CommandRecord {
	t.Helper()
	completedAt := time.Now().UTC()
	return devices.CommandRecord{
		ID:            input.ID,
		CorrelationID: input.CorrelationID,
		Status:        status,
		CompletedAt:   &completedAt,
		FailureCode:   failureCode,
		EntityID:      input.EntityID,
		OperationName: input.OperationName,
		Parameters:    append(devices.CommandParameters(nil), input.Parameters...),
	}
}
