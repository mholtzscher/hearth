package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// entityPlan is one immutable Entity description plus the functions needed to
// decode its State and Events and plan its Commands.
type entityPlan struct {
	Descriptor       adapter.EntityDescriptor
	StatePolicy      entityStatePolicy // entityStateful (default) | entityStateless
	StateProperties  []string
	GetProperties    []string
	EventProperties  []string
	DecodeState      stateDecoder
	DecodeEvent      eventDecoder
	TranslateCommand commandTranslator
}

// entityStatePolicy declares whether one planned Entity holds observable
// State. Stateless plans (read-only action Events and dispatched Command
// acts) claim no State properties, run no State decoder, and publish no
// observations, but each still carries exactly one behavior; the core
// catalog carries the matching stateless flag separately so the adapter
// never queries it.
type entityStatePolicy uint8

const (
	entityStateful entityStatePolicy = iota
	entityStateless
)

// runtimeEntity pairs one immutable plan with the canonical Entity ID returned
// by registration.
type runtimeEntity struct {
	plan     entityPlan
	entityID string
}

// stateDecoder projects one complete Entity State from a single MQTT message.
// It returns present == false when the message does not contain every State
// property required by the Entity and never saves partial values for a later
// message.
type stateDecoder func(
	entityID string,
	properties map[string]json.RawMessage,
	receivedAt time.Time,
) (stateReport, bool, error)

type stateReport struct {
	Observation adapter.Observation
	semantic    any
}

// eventDecoder projects zero or one Entity Event from one parsed MQTT object.
// It returns present == false for an absent, empty, or null source value so a
// message without a report emits no event and never replays a cached one. An
// invalid or unsupported value returns an error, which becomes a per-Entity
// issue so valid sibling State still publishes.
type eventDecoder func(
	entityID string,
	properties map[string]json.RawMessage,
) (adapter.EntityEvent, bool, error)

// stateDecodeIssue identifies one Entity whose claimed properties were all
// present but invalid, without suppressing valid siblings.
type stateDecodeIssue struct {
	Properties []string
	Err        error
}

// commandTranslator converts one typed Hearth Command into upstream set values,
// refresh properties, an absolute deadline, and a typed outcome matcher.
// A nil translator marks a read-only Entity.
type commandTranslator func(
	context.Context,
	string,
	adapter.Command,
	adapter.Responder,
) (plannedCommand, error)

type plannedCommand struct {
	SetValues     map[string]json.RawMessage
	GetProperties []string
	Deadline      time.Time
	Matches       func(stateReport) bool
	// Outcome selects the terminal path: observed Commands (default)
	// complete via fresh post-dispatch linked observation, while dispatched
	// Commands complete on adapter acceptance with no refresh, no matcher,
	// and no observation. The runtime derives the dispatched path from a
	// nil matcher with empty refresh, so a dispatched plan must leave both
	// unset.
	Outcome plannedOutcome // plannedObserved (default) | plannedDispatched
}

// plannedOutcome is the adapter-local terminal policy for one Command. It
// mirrors the core catalog outcome without depending on the catalog: the
// value is trusted from plan construction and validated at the seam.
type plannedOutcome uint8

const (
	plannedObserved plannedOutcome = iota
	plannedDispatched
)

// validateEntityPlans runs before registration. It checks descriptor
// completeness, non-empty and unique State properties, explicit Event source
// ownership, ordered get subsets, decoder presence, exactly one stateless
// behavior, duplicate Entity keys, and the controllable refresh invariant.
//
//nolint:gocognit // One function keeps every pre-registration plan invariant together.
func validateEntityPlans(plans []entityPlan) error {
	keys := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if plan.Descriptor.Key == "" || plan.Descriptor.ExternalID == "" || plan.Descriptor.Name == "" ||
			plan.Descriptor.Type == "" || len(plan.Descriptor.Support) == 0 {
			return errors.New("entity plan descriptor is incomplete")
		}
		stateless := plan.StatePolicy == entityStateless
		switch {
		case stateless:
			if err := validateStatelessPlan(plan); err != nil {
				return err
			}
		case plan.DecodeEvent != nil:
			return errors.New("stateful entity plan must not decode events")
		case len(plan.StateProperties) == 0:
			return errors.New("entity plan must claim at least one State property")
		}
		claimed := make(map[string]struct{}, len(plan.StateProperties))
		for _, property := range plan.StateProperties {
			if property == "" {
				return errors.New("entity plan State property must not be empty")
			}
			if _, duplicate := claimed[property]; duplicate {
				return errors.New("entity plan State property is duplicated")
			}
			claimed[property] = struct{}{}
		}
		seenGet := make(map[string]struct{}, len(plan.GetProperties))
		positions := make(map[string]int, len(plan.StateProperties))
		for index, property := range plan.StateProperties {
			positions[property] = index
		}
		previous := -1
		for _, property := range plan.GetProperties {
			if property == "" {
				return errors.New("entity plan get property must not be empty")
			}
			if _, duplicate := seenGet[property]; duplicate {
				return errors.New("entity plan get property is duplicated")
			}
			seenGet[property] = struct{}{}
			position, subset := positions[property]
			if !subset {
				return errors.New("entity plan get property is not a State property")
			}
			if position <= previous {
				return errors.New("entity plan get properties must follow State property order")
			}
			previous = position
		}
		if err := validateEventProperties(plan); err != nil {
			return err
		}
		if !stateless && plan.DecodeState == nil {
			return errors.New("entity plan is missing its State decoder")
		}
		if !stateless && plan.TranslateCommand != nil && len(plan.GetProperties) == 0 {
			return errors.New("controllable entity plan must declare refresh properties")
		}
		if _, duplicate := keys[plan.Descriptor.Key]; duplicate {
			return errors.New("duplicate Entity key in merged device plan")
		}
		keys[plan.Descriptor.Key] = struct{}{}
	}
	return nil
}

// validateEventProperties enforces explicit Event source ownership: a plan
// that decodes Events must name at least one non-empty, unique source
// property, and a plan without an Event decoder must name none. The names
// tell the pending replay queue which not-yet-activated messages are Event
// occurrences, so a plan that decodes Events must never silently own nothing.
func validateEventProperties(plan entityPlan) error {
	seen := make(map[string]struct{}, len(plan.EventProperties))
	for _, property := range plan.EventProperties {
		if property == "" {
			return errors.New("entity plan event property must not be empty")
		}
		if _, duplicate := seen[property]; duplicate {
			return errors.New("entity plan event property is duplicated")
		}
		seen[property] = struct{}{}
	}
	if plan.DecodeEvent != nil && len(plan.EventProperties) == 0 {
		return errors.New("entity plan with an event decoder must claim at least one event property")
	}
	if plan.DecodeEvent == nil && len(plan.EventProperties) != 0 {
		return errors.New("entity plan without an event decoder must not claim event properties")
	}
	return nil
}

// validateStatelessPlan enforces the stateless invariant: an Entity that
// claims no State carries exactly one behavior. It either decodes Events or
// translates Commands, so an action plan that silently loses its decoder can
// never register as a permanently mute Entity.
func validateStatelessPlan(plan entityPlan) error {
	if len(plan.StateProperties) != 0 || len(plan.GetProperties) != 0 || plan.DecodeState != nil {
		return errors.New("stateless entity plan must not claim State")
	}
	if (plan.DecodeEvent == nil) == (plan.TranslateCommand == nil) {
		return errors.New("stateless entity plan must declare exactly one of event decoding or command translation")
	}
	return nil
}

// validatePlannedCommand runs after typed Command translation and before MQTT
// publication. Command-specific output does not exist at discovery time.
// Active refresh is mandatory for observed Commands: every planned observed
// Command must request at least one refresh property so the runtime can
// confirm the outcome. Dispatched Commands complete on acceptance and carry
// no refresh properties and no matcher.
func validatePlannedCommand(planned plannedCommand) error {
	if len(planned.SetValues) == 0 {
		return errors.New("planned Command must set at least one property")
	}
	for property, value := range planned.SetValues {
		if property == "" || len(value) == 0 {
			return errors.New("planned Command set property must be non-empty")
		}
	}
	if planned.Outcome == plannedDispatched {
		if len(planned.GetProperties) != 0 {
			return errors.New("planned dispatched Command must not request refresh properties")
		}
		if planned.Matches != nil {
			return errors.New("planned dispatched Command must not install a matcher")
		}
		if planned.Deadline.IsZero() {
			return errors.New("planned Command deadline is required")
		}
		return nil
	}
	if len(planned.GetProperties) == 0 {
		return errors.New("planned Command must request refresh properties")
	}
	seen := make(map[string]struct{}, len(planned.GetProperties))
	for _, property := range planned.GetProperties {
		if property == "" {
			return errors.New("planned Command refresh property must not be empty")
		}
		if _, duplicate := seen[property]; duplicate {
			return errors.New("planned Command refresh property is duplicated")
		}
		seen[property] = struct{}{}
	}
	if planned.Deadline.IsZero() {
		return errors.New("planned Command deadline is required")
	}
	if planned.Matches == nil {
		return errors.New("planned Command matcher is required")
	}
	return nil
}

// exactMatcher implements outcome matching for comparable typed State. A
// semantic-type mismatch returns false and never panics.
func exactMatcher[T comparable](target T) func(stateReport) bool {
	return func(report stateReport) bool {
		value, ok := report.semantic.(T)
		return ok && value == target
	}
}

// scopedIdentity builds the canonical Entity key and display name for one
// root. Property names never enter Binding or Entity keys.
func scopedIdentity(baseKey, baseName, label string, endpoint int, scoped bool) (string, string) {
	if !scoped {
		return baseKey, baseName
	}
	endpointText := strconv.Itoa(endpoint)
	nameLabel := strings.TrimSpace(label)
	if _, err := strconv.Atoi(nameLabel); nameLabel == "" || err == nil {
		nameLabel = "ep" + endpointText
	}
	return baseKey + "-ep" + endpointText, nameLabel + " " + baseName
}
