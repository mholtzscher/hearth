package automations

import "fmt"

// Page is one keyset page of automation-owned records.
type Page[T any] struct {
	Items   []T
	HasMore bool
}

const (
	// automationDefaultPageLimit is the page size used when a caller omits one.
	automationDefaultPageLimit = 50
	// automationMaximumPageLimit bounds one definition or history page.
	automationMaximumPageLimit = 200
)

// PageLimit resolves one requested page limit to the effective page size. An
// omitted limit becomes automationDefaultPageLimit; anything outside 1 through
// automationMaximumPageLimit is an [ErrInvalidAutomation].
func PageLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return automationDefaultPageLimit, nil
	case limit < 1 || limit > automationMaximumPageLimit:
		return 0, fmt.Errorf(
			"%w: page limit must be between 1 and %d",
			ErrInvalidAutomation, automationMaximumPageLimit,
		)
	default:
		return limit, nil
	}
}
