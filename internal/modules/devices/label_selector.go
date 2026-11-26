package devices

import (
	"fmt"
	"slices"
)

// CanonicalEntitySelector returns an owned copy with Labels and types sorted
// and deduplicated, and empty sets nil, so equal selectors compare equal. It
// does not validate; pagination scopes and PrepareEntitySelector share it.
func CanonicalEntitySelector(selector EntitySelector) EntitySelector {
	labels := slices.Compact(slices.Sorted(slices.Values(selector.Labels)))
	types := slices.Compact(slices.Sorted(slices.Values(selector.Types)))
	if len(labels) == 0 {
		labels = nil
	}
	if len(types) == 0 {
		types = nil
	}
	return EntitySelector{Labels: labels, Types: types}
}

// PrepareEntitySelector validates a freely constructed selector and returns
// its canonical copy. Whether a Label exists is a changing fact checked by the
// read.
func PrepareEntitySelector(catalog *TypeCatalog, selector EntitySelector) (EntitySelector, error) {
	for _, raw := range selector.Labels {
		if _, err := ParseLabelID(string(raw)); err != nil {
			return EntitySelector{}, fmt.Errorf("%w: %w", ErrInvalidEntitySelector, err)
		}
	}
	canonical := CanonicalEntitySelector(selector)
	if len(canonical.Labels) > MaxSelectorLabels {
		return EntitySelector{}, fmt.Errorf("%w: at most %d labels", ErrInvalidEntitySelector, MaxSelectorLabels)
	}
	if len(canonical.Types) > MaxSelectorTypes {
		return EntitySelector{}, fmt.Errorf("%w: at most %d types", ErrInvalidEntitySelector, MaxSelectorTypes)
	}
	for _, typeID := range canonical.Types {
		if _, err := catalog.resolve(typeID); err != nil {
			return EntitySelector{}, fmt.Errorf("%w: %w", ErrInvalidEntitySelector, err)
		}
	}
	return canonical, nil
}
