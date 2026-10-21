package mcpapi

type CommandSummary struct {
	CommandID   string  `json:"command_id"`
	EntityID    string  `json:"entity_id"`
	Operation   string  `json:"operation"`
	Status      string  `json:"status"`
	RequestedAt string  `json:"requested_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
	FailureCode *string `json:"failure_code,omitempty"`
}
