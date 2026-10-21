package mcpapi

import "github.com/mholtzscher/hearth/internal/modules/devices"

func commandSummary(command devices.CommandRecord) CommandSummary {
	result := CommandSummary{
		CommandID: string(command.ID), EntityID: string(command.EntityID), Operation: string(command.OperationName),
		Status: string(command.Status), RequestedAt: formatTime(command.RequestedAt),
	}
	if command.CompletedAt != nil {
		value := formatTime(*command.CompletedAt)
		result.CompletedAt = &value
	}
	if command.FailureCode != nil {
		value := string(*command.FailureCode)
		result.FailureCode = &value
	}
	return result
}
