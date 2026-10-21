//nolint:goconst // Status literals make the complete domain evidence mapping explicit.
package mcpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type InvocationReceipt struct {
	InvocationID    string `json:"invocation_id"`
	AdapterID       string `json:"adapter_id"`
	FunctionKey     string `json:"function_key"`
	CatalogRevision string `json:"catalog_revision"`
	Status          string `json:"status"`
	ReasonCode      string `json:"reason_code,omitempty"`
}

type InvocationSummary struct {
	InvocationReceipt

	StartedAt   string  `json:"started_at"`
	CompletedAt *string `json:"completed_at"`
}

func invocationSummary(item adapters.Invocation) InvocationSummary {
	result := InvocationSummary{
		InvocationReceipt: InvocationReceipt{ //nolint:modernize // Keep the embedded receipt construction explicit.
			InvocationID: string(item.ID), AdapterID: item.Route.AdapterID, FunctionKey: string(item.Function.Key),
			CatalogRevision: string(item.CatalogRevision), Status: "running"},
		StartedAt: formatTime(item.StartedAt),
	}
	if item.Completion == nil {
		return result
	}
	completed := formatTime(item.Completion.At)
	result.CompletedAt = &completed
	switch evidence := item.Completion.Evidence.(type) {
	case adapters.Succeeded:
		result.Status = "succeeded"
	case adapters.Failed:
		result.Status, result.ReasonCode = "failed", evidence.ReasonCode
	case adapters.NotExecuted:
		result.Status, result.ReasonCode = "not_executed", evidence.ReasonCode
	case adapters.Unknown:
		result.Status, result.ReasonCode = "unknown", evidence.ReasonCode
	}
	return result
}
