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
	DeviceID   string `json:"device_id"`
	Field      string `json:"field"`
	Window     string `json:"window"`
	Chart      string `json:"-"`
	ValueKind  string `json:"-"`
	WithRange  bool   `json:"-"`
	WithWords  bool   `json:"-"`
}

const seriesBindingKind = "entity-series"

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
		"Metric":      metricBindings,
		"TimeSeries":  seriesBindings,
		"State":       stateBindings,
		"ReadingList": listBindings,
		"Device":      deviceBindings,
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
			if b.Kind == "device" {
				if _, idErr := devices.ParseDeviceID(b.DeviceID); idErr != nil {
					return nil, fmt.Errorf("%w: invalid device id", ErrInvalidDefinition)
				}
				ids[b.DeviceID] = true
				all = append(all, b)
				continue
			}
			if fieldErr := validateFieldBinding(b); fieldErr != nil {
				return nil, fieldErr
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
		return nil, fmt.Errorf("%w: too many entities or devices", ErrInvalidDefinition)
	}
	return all, nil
}

func metricBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding `json:"source"`
		Trend  string  `json:"trend"`
		Range  bool    `json:"range"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	props.Source.ValueKind = "numeric"
	props.Source.WithRange = props.Range
	result := []binding{props.Source}
	if props.Trend != "" {
		history := props.Source
		history.Kind, history.Window, history.WithRange = seriesBindingKind, props.Trend, false
		result = append(result, history)
	}
	return result, nil
}

func stateBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding         `json:"source"`
		Words  json.RawMessage `json:"words"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	props.Source.ValueKind, props.Source.WithWords = "text", len(props.Words) > 0
	return []binding{props.Source}, nil
}

func listBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Rows []struct {
			Source binding         `json:"source"`
			Words  json.RawMessage `json:"words"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	result := make([]binding, 0, len(props.Rows))
	for _, row := range props.Rows {
		row.Source.WithWords = len(row.Words) > 0
		result = append(result, row.Source)
	}
	return result, nil
}

func deviceBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Device binding `json:"device"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	return []binding{props.Device}, nil
}

func validateFieldBinding(b binding) error {
	field, found := devices.BuiltinStateField(devices.EntityTypeID(b.EntityType), b.Field)
	if !found {
		return fmt.Errorf("%w: unsupported State field", ErrInvalidDefinition)
	}
	numeric, isNumeric := field.Value.(devices.NumericField)
	_, isBoolean := field.Value.(devices.BooleanField)
	if ((b.ValueKind == "numeric" || b.Kind == seriesBindingKind) && !isNumeric) ||
		(b.ValueKind == "text" && isNumeric) {
		return fmt.Errorf("%w: unsupported quantity field or text field", ErrInvalidDefinition)
	}
	if b.WithWords && !isBoolean {
		return fmt.Errorf("%w: words require a boolean field", ErrInvalidDefinition)
	}
	if b.WithRange && (!isNumeric || numeric.Range == nil) {
		return fmt.Errorf("%w: field has no range", ErrInvalidDefinition)
	}
	return nil
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

func validateBindingResources(
	ctx context.Context,
	reader EntityReader,
	deviceReader DeviceReader,
	bindings []binding,
) error {
	entityBindings := make([]binding, 0, len(bindings))
	checkedDevices := make(map[string]bool)
	for _, b := range bindings {
		if b.Kind == "device" {
			if checkedDevices[b.DeviceID] {
				continue
			}
			if err := validateDeviceBinding(ctx, deviceReader, b.DeviceID); err != nil {
				return err
			}
			checkedDevices[b.DeviceID] = true
			continue
		}
		entityBindings = append(entityBindings, b)
	}
	return validateEntityBindings(ctx, reader, entityBindings)
}

func validateEntityBindings(ctx context.Context, reader EntityReader, bindings []binding) error {
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

func validateDeviceBinding(ctx context.Context, reader DeviceReader, id string) error {
	if reader == nil {
		return errors.New("device reader unavailable")
	}
	_, err := reader.GetDevice(ctx, devices.GetDeviceParams{ID: devices.DeviceID(id), EntityLimit: 1})
	if errors.Is(err, devices.ErrDeviceNotFound) {
		return fmt.Errorf("%w: device not found", ErrInvalidDefinition)
	}
	return err
}

// bindingUnit validates the selected field against the stored Entity and returns
// its canonical axis unit.
func bindingUnit(b binding, entity devices.Entity) (string, error) {
	if string(entity.TypeID) != b.EntityType || (b.Kind != "entity-state" && b.Kind != seriesBindingKind) {
		return "", fmt.Errorf("%w: entity type mismatch", ErrInvalidDefinition)
	}
	field, found := devices.BuiltinStateField(entity.TypeID, b.Field)
	if !found {
		return "", fmt.Errorf("%w: unsupported binding", ErrInvalidDefinition)
	}
	numeric, readable := field.Value.(devices.NumericField)
	if !readable {
		return "", nil
	}
	if b.WithRange {
		if _, err := devices.ResolveRange(numeric, json.RawMessage(entity.Support)); err != nil {
			return "", fmt.Errorf("%w: invalid range support", ErrInvalidDefinition)
		}
	}
	quantity, err := devices.ResolveQuantity(numeric, json.RawMessage(entity.Support))
	if err != nil {
		return "", fmt.Errorf("%w: invalid quantity support", ErrInvalidDefinition)
	}
	return quantity.Unit, nil
}
