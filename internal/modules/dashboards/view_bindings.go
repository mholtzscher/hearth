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
	Field      string `json:"field"`
	Window     string `json:"window"`
	Chart      string `json:"-"`
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
	for key, node := range graph.Elements {
		extract, ok := extractors[node.Type]
		if !ok {
			continue
		}
		bindings, err := extract(node.Props)
		if err != nil {
			return nil, err
		}
		// A chart has one axis; unit compatibility is checked against the Entities below.
		chartIDs := map[string]bool{}
		window, entityType, field := "", "", ""
		for _, b := range bindings {
			selected, found := devices.BuiltinStateField(devices.EntityTypeID(b.EntityType), b.Field)
			if _, numeric := selected.Value.(devices.NumericField); !found || !numeric {
				return nil, fmt.Errorf("%w: unsupported quantity field", ErrInvalidDefinition)
			}
			if _, idErr := devices.ParseEntityID(b.EntityID); idErr != nil {
				return nil, fmt.Errorf("%w: invalid entity id", ErrInvalidDefinition)
			}
			ids[b.EntityID] = true
			if node.Type == "TimeSeries" {
				identity := b.EntityID + ":" + b.Field
				if chartIDs[identity] || (window != "" && window != b.Window) ||
					(entityType != "" && (entityType != b.EntityType || field != b.Field)) {
					return nil, fmt.Errorf("%w: incompatible series", ErrInvalidDefinition)
				}
				chartIDs[identity] = true
				window, entityType, field = b.Window, b.EntityType, b.Field
				b.Chart = key
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
	// Devices owns canonical field interpretation. Save/present validates it
	// against current Entities, independently of whether State has been observed.
	entities := make(map[devices.EntityID]devices.EntityWithState)
	axes := make(map[string]string)
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
		unit, err := bindingUnit(b, entity.Entity)
		if err != nil {
			return err
		}
		if b.Chart != "" {
			if previous, exists := axes[b.Chart]; exists && previous != unit {
				return fmt.Errorf("%w: incompatible series units", ErrInvalidDefinition)
			}
			axes[b.Chart] = unit
		}
	}
	return nil
}

// bindingUnit validates the selected field against the stored Entity and returns
// its axis unit. Native brightness scales share a percentage axis.
func bindingUnit(b binding, entity devices.Entity) (string, error) {
	if string(entity.TypeID) != b.EntityType || (b.Kind != "entity-state" && b.Kind != "entity-series") {
		return "", fmt.Errorf("%w: entity type mismatch", ErrInvalidDefinition)
	}
	field, found := devices.BuiltinStateField(entity.TypeID, b.Field)
	numeric, readable := field.Value.(devices.NumericField)
	if !found || !readable {
		return "", fmt.Errorf("%w: unsupported binding", ErrInvalidDefinition)
	}
	quantity, err := devices.ResolveQuantity(numeric, json.RawMessage(entity.Support))
	if err != nil {
		return "", fmt.Errorf("%w: invalid quantity support", ErrInvalidDefinition)
	}
	if quantity.PercentMaximum != nil {
		return "%", nil
	}
	return quantity.Unit, nil
}
