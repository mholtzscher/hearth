package mcpapi

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	defaultPageLimit = 50
	minPageLimit     = 1
	maxPageLimit     = 200
)

type pageLimit int

func (limit *pageLimit) UnmarshalJSON(data []byte) error {
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return decodeFailure(failureInvalidLimit, err)
	}
	checked, err := pageLimitValue(value)
	if err != nil {
		return decodeFailure(failureInvalidLimit, err)
	}
	*limit = checked
	return nil
}

func decodeFailure(code failureCode, err error) error { return fmt.Errorf("%s: %w", code, err) }

func pageLimitValue(value int) (pageLimit, error) {
	if value < minPageLimit || value > maxPageLimit {
		return 0, fmt.Errorf("limit must be between %d and %d", minPageLimit, maxPageLimit)
	}
	return pageLimit(value), nil
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
		return decodeFailure(failureInvalidAdapterID, err)
	}
	if !validAdapterID(value) {
		return decodeFailure(failureInvalidAdapterID, errors.New("adapter_id must be a subject-safe slug"))
	}
	*id = adapterID(value)
	return nil
}
