package devices

import (
	"context"
	"errors"
	"fmt"
)

// ErrAutomationTriggerSource is the permanent classification for one Entity that
// cannot be the current source of the requested typed Automation Trigger
// reference. It covers a wrong-kind Entity and an unsupported Entity Event name;
// a missing Entity stays [ErrEntityNotFound]. The automations module maps both
// to its own invalid-input class.
var ErrAutomationTriggerSource = errors.New("entity is not a valid automation trigger source")

// ValidateObservationTrigger reports whether one Entity can be the source of an
// Observation Trigger: it must currently exist and be stateful. Enablement,
// availability, and owner health are deliberately not required, because
// save-time validation proves current references while normal Command
// execution-time validation handles control eligibility.
func (service *Service) ValidateObservationTrigger(ctx context.Context, entityID EntityID) error {
	if _, err := ParseEntityID(string(entityID)); err != nil {
		return fmt.Errorf("%w: parse entity ID: %w", ErrAutomationTriggerSource, err)
	}
	view, err := service.stores.Reads.GetEntity(ctx, entityID)
	if err != nil {
		return err
	}
	stateless, err := service.catalog.IsStateless(view.Entity.TypeID)
	if err != nil {
		return fmt.Errorf("resolve entity type %q: %w", view.Entity.TypeID, err)
	}
	if stateless {
		return fmt.Errorf(
			"%w: stateless entity %q has no State to observe",
			ErrAutomationTriggerSource, entityID,
		)
	}
	return nil
}

// ValidateEntityEventTrigger reports whether one Entity can be the source of an
// Entity Event Trigger: it must currently exist and support the exact event
// name. Enablement, availability, and owner health are deliberately not
// required, matching [Service.ValidateObservationTrigger].
func (service *Service) ValidateEntityEventTrigger(
	ctx context.Context,
	entityID EntityID,
	name EntityEventName,
) error {
	if _, err := ParseEntityID(string(entityID)); err != nil {
		return fmt.Errorf("%w: parse entity ID: %w", ErrAutomationTriggerSource, err)
	}
	view, err := service.stores.Reads.GetEntity(ctx, entityID)
	if err != nil {
		return err
	}
	supported, err := service.catalog.SupportsEntityEvent(view.Entity, name)
	if err != nil {
		return fmt.Errorf("interpret entity event support for %q: %w", entityID, err)
	}
	if !supported {
		return fmt.Errorf(
			"%w: entity %q does not support event %q",
			ErrAutomationTriggerSource, entityID, name,
		)
	}
	return nil
}
