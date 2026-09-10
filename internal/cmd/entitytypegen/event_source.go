package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// entityEventNamePattern is the one canonical Entity Event name shape. Wire
// schemas, generated name validation, and the event-source support schema all
// use it, so a supported name can always travel verbatim.
const entityEventNamePattern = `^[a-z0-9][a-z0-9_-]{0,62}$`

const (
	entityEventNamesMinimum = 1
	entityEventNamesMaximum = 64
	entityEventNameLength   = 63
)

// requireEntityEventSupportSchema validates the fixed event-source support
// shape and returns the support.events schema. Event support is closed and
// name-only: an event-source Entity reports named occurrences, so it supports
// no State space and no Operations.
func requireEntityEventSupportSchema(support schemaNode) (schemaNode, error) {
	events, declaresEvents := support.Properties["events"]
	if !declaresEvents {
		return schemaNode{}, errors.New("event_source requires a support.events property")
	}
	if !required(support, "events") {
		return schemaNode{}, errors.New(`event_source requires support to require "events"`)
	}
	if events.Type != schemaTypeObject {
		return schemaNode{}, errors.New("support.events must describe an object")
	}
	if err := requireClosedObject(events); err != nil {
		return schemaNode{}, fmt.Errorf("support.events schema: %w", err)
	}
	for _, property := range sortedProperties(events.Properties) {
		if property != "names" {
			return schemaNode{}, fmt.Errorf("support.events has unsupported property %q", property)
		}
	}
	names, declaresNames := events.Properties["names"]
	if !declaresNames {
		return schemaNode{}, errors.New("support.events requires a names property")
	}
	if !required(events, "names") {
		return schemaNode{}, errors.New(`support.events must require "names"`)
	}
	if names.Type != schemaTypeArray {
		return schemaNode{}, errors.New("support.events.names must describe an array")
	}
	if names.UniqueItems == nil || !*names.UniqueItems {
		return schemaNode{}, errors.New("support.events.names must set uniqueItems")
	}
	if names.MinItems == nil || *names.MinItems != entityEventNamesMinimum ||
		names.MaxItems == nil || *names.MaxItems != entityEventNamesMaximum {
		return schemaNode{}, fmt.Errorf(
			"support.events.names must accept %d to %d names",
			entityEventNamesMinimum,
			entityEventNamesMaximum,
		)
	}
	items := names.Items
	if items == nil || items.Type != string(kindString) {
		return schemaNode{}, errors.New("support.events.names must describe a string array")
	}
	if items.Pattern != entityEventNamePattern {
		return schemaNode{}, fmt.Errorf(
			"support.events.names items must use pattern %s",
			entityEventNamePattern,
		)
	}
	if items.MinLength == nil || *items.MinLength != 1 ||
		items.MaxLength == nil || *items.MaxLength != entityEventNameLength {
		return schemaNode{}, fmt.Errorf(
			"support.events.names items must be 1 to %d characters",
			entityEventNameLength,
		)
	}
	return events, nil
}

// requireEventSourceStateShape validates the fixed no-State shape of an
// event-source Entity. The type reports named occurrences only, so the State
// schema and the support.state member it publishes must both describe closed
// empty objects. support.state is already required by the shared support
// schema rule; a State space or extra support members would let an event source
// smuggle in a State surface the generated facade defines no observation for.
func requireEventSourceStateShape(state, stateSupport schemaNode) error {
	if err := requireEmptyClosedObject(state, "event_source state schema"); err != nil {
		return err
	}
	if err := requireEmptyClosedObject(stateSupport, "event_source support.state"); err != nil {
		return err
	}
	return nil
}

// requireEmptyClosedObject enforces an empty fixed object: it must describe an
// object, declare no properties, and reject unknown members.
func requireEmptyClosedObject(schema schemaNode, description string) error {
	if schema.Type != schemaTypeObject {
		return fmt.Errorf("%s must describe an object", description)
	}
	if len(schema.Properties) != 0 {
		return fmt.Errorf("%s must declare no properties", description)
	}
	if err := requireClosedObject(schema); err != nil {
		return fmt.Errorf("%s: %w", description, err)
	}
	return nil
}

// entityEventNamesFromSupport reads the authored supported names out of one
// example support value. Generation only reads names to pick deterministic
// probes; the generated contract and catalog validators own real membership.
func entityEventNamesFromSupport(raw json.RawMessage) ([]string, error) {
	var shape struct {
		Events *struct {
			Names []string `json:"names"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, err
	}
	if shape.Events == nil || len(shape.Events.Names) == 0 {
		return nil, errors.New("support has no events.names")
	}
	return shape.Events.Names, nil
}

// requireEventSourceExamples keeps the generated Entity Event probes honest:
// every authored case must declare at least one supported name, so the
// generated conformance tests always have a real supported and unsupported
// name to compare.
func requireEventSourceExamples(examples examplesFile) error {
	for index, example := range examples.Cases {
		if _, err := entityEventNamesFromSupport(example.Support); err != nil {
			return fmt.Errorf("event_source case %d: %w", index+1, err)
		}
	}
	return nil
}

// supportWithEvents adds an Entity Event member to an authored support, so a
// generated probe can prove a closed Entity type rejects event support even
// though the shared registration schema can carry it.
func supportWithEvents(raw json.RawMessage) (json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value["events"] = json.RawMessage(`{"names":["single_press"]}`)
	compacted, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(compacted), nil
}

// unsupportedEntityEventName returns a deterministic canonical name absent
// from the supported names, so generated rejection probes cannot collide with
// a real name.
func unsupportedEntityEventName(names []string) string {
	for _, candidate := range []string{"unknown", "unknown-event", "unsupported"} {
		if !slices.Contains(names, candidate) {
			return candidate
		}
	}
	for index := 2; ; index++ {
		candidate := "unknown-event-" + strconv.Itoa(index)
		if !slices.Contains(names, candidate) {
			return candidate
		}
	}
}

// supportWithoutEvents removes the events member from an authored support,
// producing the schema-invalid descriptor that generated catalog probes use to
// prove corrupt event support is a catalog failure rather than an unsupported
// name.
func supportWithoutEvents(raw json.RawMessage) (json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if _, exists := value["events"]; !exists {
		return nil, errors.New("support has no events member")
	}
	delete(value, "events")
	compacted, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(compacted), nil
}
