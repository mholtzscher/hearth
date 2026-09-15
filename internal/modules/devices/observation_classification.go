package devices

import "fmt"

// ClassifyObservation normalizes an incoming State value and determines whether
// it changes the current State. A prior ownership, runtime, or enablement
// rejection takes precedence. Persistence must pass the Entity and State loaded
// inside its projection transaction; classification performs no reads or writes.
func ClassifyObservation(
	catalog *TypeCatalog,
	view EntityWithState,
	value Value,
	rejection *ObservationRejection,
) (Value, ObservationDisposition, *ObservationRejection, error) {
	if rejection != nil {
		return nil, DispositionRejected, rejection, nil
	}
	if stateless, err := catalog.IsStateless(view.Entity.TypeID); err != nil {
		return nil, "", nil, err
	} else if stateless {
		invalid := RejectionInvalidValue
		return nil, DispositionRejected, &invalid, nil
	}
	if _, err := catalog.NormalizeSupport(view.Entity.TypeID, view.Entity.Support); err != nil {
		return nil, "", nil, fmt.Errorf("validate persisted entity support: %w", err)
	}
	normalized, err := catalog.NormalizeState(view.Entity, value)
	if err != nil {
		invalid := RejectionInvalidValue
		return nil, DispositionRejected, &invalid, nil //nolint:nilerr // Invalid input is a durable rejection, not a processing failure.
	}
	if view.State == nil {
		return normalized, DispositionApplied, nil, nil
	}
	equal, err := catalog.EqualState(view.Entity, view.State.Value, normalized)
	if err != nil {
		return nil, "", nil, fmt.Errorf("compare observation state: %w", err)
	}
	if equal {
		return normalized, DispositionUnchanged, nil, nil
	}
	return normalized, DispositionApplied, nil, nil
}
