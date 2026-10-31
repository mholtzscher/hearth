package mcpapi

import (
	"encoding/json"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

const (
	defaultPageLimit = 50
	minPageLimit     = 1
	maxPageLimit     = 200

	invalidAdapterIDMessage = "adapter_id must be a subject-safe slug"
)

type pageLimit int

func (limit *pageLimit) UnmarshalJSON(data []byte) error {
	value, err := mcptools.DecodeInteger(data)
	if err != nil || value < minPageLimit || value > maxPageLimit {
		return &mcptools.ToolError{
			Code:    string(failureInvalidLimit),
			Message: "limit must be an integer between 1 and 200",
		}
	}
	*limit = pageLimit(value)
	return nil
}

func pageSize(limit pageLimit) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return int(limit)
}

type PageInput struct {
	Limit  pageLimit `json:"limit,omitempty"  doc:"page size from 1 to 200; defaults to 50"`
	Cursor string    `json:"cursor,omitempty" doc:"opaque cursor from a previous page's next_cursor"`
}

type adapterID string

func (id *adapterID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return &mcptools.ToolError{Code: string(failureInvalidAdapterID), Message: invalidAdapterIDMessage}
	}
	if !validAdapterID(value) {
		return &mcptools.ToolError{Code: string(failureInvalidAdapterID), Message: invalidAdapterIDMessage}
	}
	*id = adapterID(value)
	return nil
}
