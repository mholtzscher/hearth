package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrAutomationTriggerSource is the permanent classification for one Entity that
// cannot be the current source of the requested typed Automation Trigger
// reference. It covers a wrong-kind Entity and an unsupported Entity Event name;
// a missing Entity stays [ErrEntityNotFound]. The automations module maps both
// to its own invalid-input class.
var ErrAutomationTriggerSource = errors.New("entity is not a valid automation trigger source")

// ErrAutomationConditionEntity is the permanent classification for one Entity
// that cannot be the current source of an Automation Condition reference: it
// exists but is stateless, so it has no State a Condition could select. A
// missing Entity stays [ErrEntityNotFound]. The automations module maps both to
// its own invalid-input class, so a definition referencing either is rejected at
// save time.
var ErrAutomationConditionEntity = errors.New("entity is not a valid automation condition reference")

// errStatefulEntityRequired is the private shared classification for one Entity
// that exists but has no State. Each public save-time validator translates it
// into its own permanent class, so callers never depend on this internal one.
var errStatefulEntityRequired = errors.New("entity has no State")

// ValidateObservationTrigger reports whether one Entity can be the source of an
// Observation Trigger: it must currently exist and be stateful. Enablement,
// availability, and owner health are deliberately not required, because
// save-time validation proves current references while normal Command
// execution-time validation handles control eligibility.
func (service *Service) ValidateObservationTrigger(
	ctx context.Context,
	entityID EntityID,
	pointers []string,
) error {
	if _, err := ParseEntityID(string(entityID)); err != nil {
		return fmt.Errorf("%w: parse entity ID: %w", ErrAutomationTriggerSource, err)
	}
	if err := service.validateStatefulEntityReference(ctx, entityID); err != nil {
		if errors.Is(err, errStatefulEntityRequired) {
			return fmt.Errorf(
				"%w: stateless entity %q has no State to observe",
				ErrAutomationTriggerSource, entityID,
			)
		}
		return err
	}
	if len(pointers) == 0 {
		return nil
	}
	view, err := service.stores.Reads.GetEntity(ctx, entityID)
	if err != nil {
		return err
	}
	if view.State == nil {
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(view.State.Value))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		return fmt.Errorf("read current Observation value for entity %q: %w", entityID, err)
	}
	for _, pointer := range pointers {
		if observationValuePointerExists(value, pointer) {
			continue
		}
		return fmt.Errorf(
			"%w: entity %q comparison pointer %q cannot select from the current Observation value; "+
				"pointers address the value directly, so use an empty pointer for a scalar and never /state/value",
			ErrAutomationTriggerSource, entityID, pointer,
		)
	}
	return nil
}

// observationValuePointerExists reports whether a validated RFC 6901 pointer
// selects a member of one decoded Observation value.
func observationValuePointerExists(value any, pointer string) bool {
	if pointer == "" {
		return true
	}
	current := value
	for encoded := range strings.SplitSeq(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch typed := current.(type) {
		case map[string]any:
			var exists bool
			current, exists = typed[token]
			if !exists {
				return false
			}
		case []any:
			index, valid := observationArrayIndex(token)
			if !valid || index >= len(typed) {
				return false
			}
			current = typed[index]
		default:
			return false
		}
	}
	return true
}

// observationArrayIndex parses the same canonical non-negative decimal token
// accepted by runtime Observation comparison matching.
func observationArrayIndex(token string) (int, bool) {
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, false
	}
	for index := range len(token) {
		if token[index] < '0' || token[index] > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(token)
	return value, err == nil
}

// ValidateConditionEntity reports whether one Entity can be the source of an
// Automation Condition: it must currently exist and be stateful. It shares the
// private stateful-Entity reference rule with [Service.ValidateObservationTrigger]
// without widening Trigger semantics.
//
// Save-time validation deliberately does not require a present State, a
// compatible current selected value, availability, enablement, or a healthy
// owner: those are evaluation results, not definition errors, and retained
// State must not be reinterpreted against current support. A missing Entity
// stays [ErrEntityNotFound]; a stateless Entity is [ErrAutomationConditionEntity].
func (service *Service) ValidateConditionEntity(ctx context.Context, entityID EntityID) error {
	if _, err := ParseEntityID(string(entityID)); err != nil {
		return fmt.Errorf("%w: parse entity ID: %w", ErrAutomationConditionEntity, err)
	}
	if err := service.validateStatefulEntityReference(ctx, entityID); err != nil {
		if errors.Is(err, errStatefulEntityRequired) {
			return fmt.Errorf(
				"%w: stateless entity %q has no State to compare",
				ErrAutomationConditionEntity, entityID,
			)
		}
		return err
	}
	return nil
}

// validateStatefulEntityReference implements the save-time reference rule shared
// by Observation Trigger sources and Condition Entity references: the Entity
// must currently exist and be stateful. A missing Entity returns
// [ErrEntityNotFound] unchanged; a stateless Entity returns
// [errStatefulEntityRequired]; an unreadable Entity type returns the catalog
// failure. Enablement, availability, and owner health are never consulted, so a
// disabled or unavailable Entity still proves a current reference.
func (service *Service) validateStatefulEntityReference(ctx context.Context, entityID EntityID) error {
	view, err := service.stores.Reads.GetEntity(ctx, entityID)
	if err != nil {
		return err
	}
	stateless, err := service.catalog.IsStateless(view.Entity.TypeID)
	if err != nil {
		return fmt.Errorf("resolve entity type %q: %w", view.Entity.TypeID, err)
	}
	if stateless {
		return fmt.Errorf("%w: entity %q", errStatefulEntityRequired, entityID)
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
