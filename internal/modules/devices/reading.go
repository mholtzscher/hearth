package devices

import "slices"

// ReadingKind names what a dashboard can read from an Entity type's State.
type ReadingKind string

// ReadingQuantity is a scalar numeric State with a canonical unit.
const ReadingQuantity ReadingKind = "quantity"

// Reading is the generated presentation contract for one Entity type, declared
// by its manifest and resolved against contracts/quantities/v1. FixedUnit and
// SupportUnitPath are mutually exclusive.
type Reading struct {
	Kind            ReadingKind
	Quantity        string
	FixedUnit       string
	SupportUnitPath string
	DefaultDisplay  string
	Displays        []string // sorted display keys
}

// AcceptsDisplay reports whether a persisted display key is valid for this reading.
func (r Reading) AcceptsDisplay(key string) bool {
	_, found := slices.BinarySearch(r.Displays, key)
	return found
}
