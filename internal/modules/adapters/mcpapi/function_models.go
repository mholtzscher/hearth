package mcpapi

import "github.com/mholtzscher/hearth/internal/modules/adapters"

type FunctionSummary struct {
	ContractFingerprint string `json:"contract_fingerprint" pattern:"^sha256-v1:[0-9a-f]{64}$"`
	AdapterID           string `json:"adapter_id"           pattern:"^[a-z0-9][a-z0-9_-]{0,62}$"`
	FunctionKey         string `json:"function_key"         pattern:"^[a-z0-9][a-z0-9_-]{0,62}$"`
	CatalogRevision     string `json:"catalog_revision"     pattern:"^afc_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
	Description         string `json:"description"          pattern:"[^\\x{0009}-\\x{000d}\\x{0020}\\x{0085}\\x{00a0}\\x{1680}\\x{2000}-\\x{200a}\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}]" minLength:"1" maxLength:"2000"`
	Callable            bool   `json:"callable"`
	TimeoutMS           int    `json:"timeout_ms"                                                                                                                                                                               minimum:"100" maximum:"60000"`
}

func functionSummary(item adapters.Function) FunctionSummary {
	return FunctionSummary{AdapterID: item.AdapterID, FunctionKey: string(item.Descriptor.Key),
		ContractFingerprint: string(item.ContractFingerprint),
		CatalogRevision:     string(item.CatalogRevision), Description: item.Descriptor.Description,
		Callable: item.Callable, TimeoutMS: item.Descriptor.TimeoutMS}
}
