package mcpapi

import (
	"encoding/json"
	"errors"
)

// The argument types below mirror the Devices MCP custom scalars: a dedicated
// type with an UnmarshalJSON decoder rejects a malformed scalar while the SDK
// decodes tool arguments, so no automation handler or service call ever sees one.
// The stable scalar failure code prefixes every message,
// because the SDK renders a decoding failure as the tool result text and that
// text is the only place the code crosses the MCP boundary.

// pageLimit is a page-size argument bounded to the range the Huma query
// parameter accepts. Decoding rejects an out-of-range value; the zero value means
// the argument was omitted.
type pageLimit int

func (limit *pageLimit) UnmarshalJSON(data []byte) error {
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("invalid_limit: limit must be an integer")
	}
	if value < 1 || value > pageMaximumLimit {
		return errors.New("invalid_limit: limit must be between 1 and 200")
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
