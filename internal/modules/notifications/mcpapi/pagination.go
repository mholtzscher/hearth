package mcpapi

const defaultPageLimit = 50

func pageLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}
