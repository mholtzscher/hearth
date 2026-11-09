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
	// BooleanOnly rejects text fields: a WeekPattern measures time true.
	BooleanOnly bool `json:"-"`
	// SameDeviceAs ties a ToggleTile's numeric reading to its power Entity.
	SameDeviceAs string `json:"-"`
	// RequiredUnit pins a weather card's numeric-sensor slot to one stored unit:
	// the Entity type alone cannot tell rain from wind direction.
	RequiredUnit string `json:"-"`
}

const (
	seriesBindingKind = "entity-series"
	// numericValueKind marks a binding that reads a numeric field.
	numericValueKind = "numeric"
	// textValueKind marks a binding that reads a yes/no or text field.
	textValueKind        = "text"
	powerMetricValueKind = "power-metric"
)

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
		"Metric":        metricBindings,
		"TimeSeries":    seriesBindings,
		"State":         stateBindings,
		"ReadingList":   listBindings,
		"Device":        deviceBindings,
		"StateTimeline": timelineBindings,
		"WeekPattern":   weekPatternBindings,
		"ToggleTile":    toggleTileBindings,
		"ToggleList":    toggleListBindings,
		"OutsideNow":    outsideNowBindings,
		"Rain":          rainBindings,
		"Wind":          windBindings,
		"Pressure":      pressureBindings,
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
		rowIDs := map[string]bool{}
		window, entityType, field := "", "", ""
		for _, b := range bindings {
			if b.Kind == "device" {
				if _, idErr := devices.ParseDeviceID(b.DeviceID); idErr != nil {
					return nil, invalidDefinition("device_id must be a canonical Device ID")
				}
				ids[b.DeviceID] = true
				all = append(all, b)
				continue
			}
			if fieldErr := validateFieldBinding(b); fieldErr != nil {
				return nil, fieldErr
			}
			if _, idErr := devices.ParseEntityID(b.EntityID); idErr != nil {
				return nil, invalidDefinition("entity_id must be a canonical Entity ID")
			}
			ids[b.EntityID] = true
			if node.Type == "TimeSeries" {
				identity := b.EntityID + ":" + b.Field
				if chartIDs[identity] || (window != "" && window != b.Window) ||
					(entityType != "" && (entityType != b.EntityType || field != b.Field)) {
					return nil, invalidDefinition(
						"a TimeSeries charts distinct Entities of one entity_type and field over one window",
					)
				}
				chartIDs[identity] = true
				window, entityType, field = b.Window, b.EntityType, b.Field
				b.Chart = key
			}
			if node.Type == "StateTimeline" {
				identity := b.EntityID + ":" + b.Field
				if rowIDs[identity] {
					return nil, invalidDefinition("a StateTimeline shows each Entity field once")
				}
				rowIDs[identity] = true
			}
			if node.Type == "ToggleList" {
				if rowIDs[b.EntityID] {
					return nil, invalidDefinition("a ToggleList shows each power Entity once")
				}
				rowIDs[b.EntityID] = true
			}
			all = append(all, b)
		}
	}
	const maxEntities = 16
	if len(ids) > maxEntities {
		return nil, invalidDefinition("a view references at most 16 distinct Entities and Devices")
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
	props.Source.ValueKind = numericValueKind
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
	props.Source.ValueKind, props.Source.WithWords = textValueKind, len(props.Words) > 0
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

func timelineBindings(raw json.RawMessage) ([]binding, error) {
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
		row.Source.ValueKind, row.Source.WithWords = textValueKind, len(row.Words) > 0
		result = append(result, row.Source)
	}
	return result, nil
}

func weekPatternBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding         `json:"source"`
		Words  json.RawMessage `json:"words"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	props.Source.ValueKind, props.Source.WithWords, props.Source.BooleanOnly = textValueKind, len(props.Words) > 0, true
	return []binding{props.Source}, nil
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

func toggleTileBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding `json:"source"`
		Metric *struct {
			Source binding `json:"source"`
			Window string  `json:"window"`
			Detail *struct {
				Source binding `json:"source"`
			} `json:"detail"`
		} `json:"metric"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	props.Source.ValueKind = textValueKind
	result := []binding{props.Source}
	if props.Metric != nil {
		metric := props.Metric.Source
		metric.ValueKind, metric.SameDeviceAs = powerMetricValueKind, props.Source.EntityID
		history := metric
		history.Kind, history.Window = seriesBindingKind, props.Metric.Window
		result = append(result, metric, history)
		if props.Metric.Detail != nil {
			detail := props.Metric.Detail.Source
			detail.ValueKind, detail.SameDeviceAs = powerMetricValueKind, props.Source.EntityID
			result = append(result, detail)
		}
	}
	return result, nil
}

func toggleListBindings(raw json.RawMessage) ([]binding, error) {
	bindings, err := listBindings(raw)
	if err != nil {
		return nil, err
	}
	for i := range bindings {
		bindings[i].ValueKind = textValueKind
	}
	return bindings, nil
}

func validateFieldBinding(b binding) error {
	field, found := devices.BuiltinStateField(devices.EntityTypeID(b.EntityType), b.Field)
	if !found {
		return invalidDefinition("the field is not a State field of the entity_type")
	}
	numeric, isNumeric := field.Value.(devices.NumericField)
	_, isBoolean := field.Value.(devices.BooleanField)
	if b.ValueKind == powerMetricValueKind && !isNumeric {
		return invalidDefinition("ToggleTile metrics and details need numeric State fields")
	}
	if b.BooleanOnly && !isBoolean {
		return invalidDefinition("WeekPattern needs a yes/no field")
	}
	if ((b.ValueKind == numericValueKind || b.Kind == seriesBindingKind) && !isNumeric) ||
		(b.ValueKind == textValueKind && isNumeric) {
		return invalidDefinition(
			"Metric and TimeSeries need a numeric field; State and StateTimeline need a yes/no or text field",
		)
	}
	if b.WithWords && !isBoolean {
		return invalidDefinition("words need a yes/no field")
	}
	if b.WithRange && (!isNumeric || numeric.Range == nil) {
		return invalidDefinition("range needs a numeric field that declares bounds")
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
				return invalidDefinition("an entity_id names no Entity")
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
				return invalidDefinition("a TimeSeries charts series that share one unit")
			}
			axes[b.Chart] = unit
		}
	}
	return validatePowerDevices(bindings, entities)
}

// validatePowerDevices keeps a tile's measurements attached to the Device it controls.
func validatePowerDevices(bindings []binding, entities map[devices.EntityID]devices.EntityWithState) error {
	for _, b := range bindings {
		if b.SameDeviceAs != "" &&
			entities[devices.EntityID(b.EntityID)].Entity.DeviceID != entities[devices.EntityID(b.SameDeviceAs)].Entity.DeviceID {
			return invalidDefinition("ToggleTile metrics and details must belong to the power Entity's Device")
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
		return invalidDefinition("a device_id names no Device")
	}
	return err
}

// bindingUnit validates the selected field against the stored Entity and returns
// its canonical axis unit.
func bindingUnit(b binding, entity devices.Entity) (string, error) {
	if string(entity.TypeID) != b.EntityType || (b.Kind != "entity-state" && b.Kind != seriesBindingKind) {
		return "", invalidDefinition("entity_type must match the Entity, with kind entity-state or entity-series")
	}
	field, found := devices.BuiltinStateField(entity.TypeID, b.Field)
	if !found {
		return "", invalidDefinition("the field is not a State field of the Entity")
	}
	numeric, readable := field.Value.(devices.NumericField)
	if !readable {
		return "", nil
	}
	if b.WithRange {
		if _, err := devices.ResolveRange(numeric, json.RawMessage(entity.Support)); err != nil {
			return "", invalidDefinition("the Entity declares no usable range")
		}
	}
	quantity, err := devices.ResolveQuantity(numeric, json.RawMessage(entity.Support))
	if err != nil {
		return "", invalidDefinition("the Entity declares no usable unit")
	}
	if b.RequiredUnit != "" && quantity.Unit != b.RequiredUnit {
		return "", invalidDefinition(weatherUnitReasons[b.RequiredUnit])
	}
	return quantity.Unit, nil
}
