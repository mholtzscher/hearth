package mcpapi

import mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"

// pageLimit is a page-size argument bounded to the range the Huma query
// parameter accepts. Decoding rejects an out-of-range value; the zero value means
// the argument was omitted.
type pageLimit int

func (limit *pageLimit) UnmarshalJSON(data []byte) error {
	value, err := mcptools.DecodeInteger(data)
	if err != nil {
		return &mcptools.ToolError{Code: "invalid_limit", Message: "limit must be an integer"}
	}
	if value < 1 || value > pageMaximumLimit {
		return &mcptools.ToolError{Code: "invalid_limit", Message: "limit must be between 1 and 200"}
	}
	*limit = pageLimit(value)
	return nil
}

func (limit pageLimit) pageSize() int {
	if limit == 0 {
		return pageDefaultLimit
	}
	return int(limit)
}

const pageDefaultLimit = 50

const pageMaximumLimit = 200
