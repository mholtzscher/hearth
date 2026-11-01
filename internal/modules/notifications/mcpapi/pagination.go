package mcpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

const defaultPageLimit = 50

type limitArgument int

func (limit *limitArgument) UnmarshalJSON(raw []byte) error {
	value, err := mcptools.DecodeInteger(raw)
	if err != nil || value < 1 || value > 200 {
		return toolError(notifications.ErrInvalidInput)
	}
	*limit = limitArgument(value)
	return nil
}

func pageLimit(limit limitArgument) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return int(limit)
}
