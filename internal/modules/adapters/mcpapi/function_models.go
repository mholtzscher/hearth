package mcpapi

import "github.com/mholtzscher/hearth/internal/modules/adapters"

type FunctionSummary struct {
	AdapterID       string `json:"adapter_id"`
	FunctionKey     string `json:"function_key"`
	CatalogRevision string `json:"catalog_revision"`
	Description     string `json:"description"`
	Callable        bool   `json:"callable"`
	TimeoutMS       int    `json:"timeout_ms"`
}

func functionSummary(item adapters.Function) FunctionSummary {
	return FunctionSummary{AdapterID: item.AdapterID, FunctionKey: string(item.Descriptor.Key),
		CatalogRevision: string(item.CatalogRevision), Description: item.Descriptor.Description,
		Callable: item.Callable, TimeoutMS: item.Descriptor.TimeoutMS}
}
