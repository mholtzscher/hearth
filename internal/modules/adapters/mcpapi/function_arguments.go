package mcpapi

type functionInput struct {
	AdapterID   string `json:"adapter_id"`
	FunctionKey string `json:"function_key"`
}
type FunctionPageInput struct {
	AdapterID string    `json:"adapter_id"`
	Limit     pageLimit `json:"limit,omitempty"  minimum:"1" maximum:"200" default:"50"`
	Cursor    string    `json:"cursor,omitempty"`
}

func functionPageLimit(limit pageLimit) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return int(limit)
}
