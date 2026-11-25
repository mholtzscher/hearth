package devices

import (
	"fmt"
	"slices"
)

// PrepareEntitySelector validates a freely constructed selector and returns an
// owned canonical copy: Labels and types are deduplicated and sorted, so equal
// selectors compare equal and scope pagination identically. Whether a Label
// exists is a changing fact checked by the read.
func PrepareEntitySelector(catalog *TypeCatalog, selector EntitySelector) (EntitySelector, error) {
	labels := make([]LabelID, 0, len(selector.Labels))
	for _, raw := range selector.Labels {
		id, err := ParseLabelID(string(raw))
		if err != nil {
			return EntitySelector{}, fmt.Errorf("%w: %w", ErrInvalidEntitySelector, err)
		}
		labels = append(labels, id)
	}
	slices.Sort(labels)
	labels = slices.Compact(labels)
	if len(labels) > MaxSelectorLabels {
		return EntitySelector{}, fmt.Errorf("%w: at most %d labels", ErrInvalidEntitySelector, MaxSelectorLabels)
	}
	types := slices.Clone(selector.Types)
	slices.Sort(types)
	types = slices.Compact(types)
	if len(types) > MaxSelectorTypes {
		return EntitySelector{}, fmt.Errorf("%w: at most %d types", ErrInvalidEntitySelector, MaxSelectorTypes)
	}
	for _, typeID := range types {
		if _, err := catalog.resolve(typeID); err != nil {
			return EntitySelector{}, fmt.Errorf("%w: %w", ErrInvalidEntitySelector, err)
		}
	}
	if len(labels) == 0 {
		labels = nil
	}
	if len(types) == 0 {
		types = nil
	}
	return EntitySelector{Labels: labels, Types: types}, nil
}
