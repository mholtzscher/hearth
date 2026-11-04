package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type binding struct {
	Kind       string `json:"kind"`
	EntityID   string `json:"entity_id"`
	EntityType string `json:"entity_type"`
	Window     string `json:"window"`
}

// collectBindings runs only after schema and graph validation. Extractors are
// registered per component; arbitrary properties are never interpreted as reads.
//
//nolint:gocognit // Binding limits and chart compatibility are checked together.
func collectBindings(raw json.RawMessage) ([]binding, error) {
	var graph viewGraph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return nil, err
	}
	extractors := map[string]func(json.RawMessage) ([]binding, error){
		"Metric":     metricBindings,
		"TimeSeries": seriesBindings,
	}
	var all []binding
	ids := map[string]bool{}
	for _, node := range graph.Elements {
		extract, ok := extractors[node.Type]
		if !ok {
			continue
		}
		bindings, err := extract(node.Props)
		if err != nil {
			return nil, err
		}
		// One chart has one value axis, so its series share a window and an Entity type.
		chartIDs := map[string]bool{}
		window, entityType := "", ""
		for _, b := range bindings {
			if _, idErr := devices.ParseEntityID(b.EntityID); idErr != nil {
				return nil, fmt.Errorf("%w: invalid entity id", ErrInvalidDefinition)
			}
			ids[b.EntityID] = true
			if node.Type == "TimeSeries" {
				if chartIDs[b.EntityID] || (window != "" && window != b.Window) ||
					(entityType != "" && entityType != b.EntityType) {
					return nil, fmt.Errorf("%w: incompatible series", ErrInvalidDefinition)
				}
				chartIDs[b.EntityID] = true
				window = b.Window
				entityType = b.EntityType
			}
			all = append(all, b)
		}
	}
	const maxEntities = 16
	if len(ids) > maxEntities {
		return nil, fmt.Errorf("%w: too many entities", ErrInvalidDefinition)
	}
	return all, nil
}

func metricBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding `json:"source"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	return []binding{props.Source}, nil
}

func seriesBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Series []struct {
			Source binding `json:"source"`
		} `json:"series"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	result := make([]binding, 0, len(props.Series))
	for _, item := range props.Series {
		result = append(result, item.Source)
	}
	return result, nil
}

func validateBindingResources(ctx context.Context, reader EntityReader, bindings []binding) error {
	// Devices validates Support on admission, so binding validation checks only
	// that the Entity exists with the bound type and that the type declares a
	// reading. Never-observed and unavailable Entities remain bindable.
	entities := make(map[devices.EntityID]devices.EntityWithState)
	for _, b := range bindings {
		id := devices.EntityID(b.EntityID)
		entity, ok := entities[id]
		if !ok {
			if reader == nil {
				return fmt.Errorf("entity reader unavailable")
			}
			var err error
			entity, err = reader.GetEntity(ctx, id)
			if errors.Is(err, devices.ErrEntityNotFound) {
				return fmt.Errorf("%w: entity not found", ErrInvalidDefinition)
			}
			if err != nil {
				return err
			}
			entities[id] = entity
		}
		_, readable := devices.BuiltinReading(entity.Entity.TypeID)
		if string(entity.Entity.TypeID) != b.EntityType || (b.Kind != "entity-state" && b.Kind != "entity-series") {
			return fmt.Errorf("%w: entity type mismatch", ErrInvalidDefinition)
		}
		if !readable {
			return fmt.Errorf("%w: unsupported binding", ErrInvalidDefinition)
		}
	}
	return nil
}
