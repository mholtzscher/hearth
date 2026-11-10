package devices

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mholtzscher/hearth/entitytypes/quantitykind"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

// ReadUnits selects a response representation, never write-input interpretation.
type ReadUnits string

const UnitsCanonical ReadUnits = "canonical"

func ParseReadUnits(value string) (ReadUnits, error) {
	if value != "" && value != string(UnitsCanonical) {
		return "", errors.New("units must be omitted or canonical")
	}
	return ReadUnits(value), nil
}

type QuantityMetadata struct {
	Unit           string          `json:"unit"`
	Label          string          `json:"label"`
	FractionDigits int             `json:"fraction_digits"`
	Range          *QuantityExtent `json:"range,omitempty"`
}

type QuantityExtent struct {
	Minimum float64 `json:"minimum"`
	Maximum float64 `json:"maximum"`
}

// Presenter owns field policy shared by HTTP and MCP. Services and persistence
// continue to return canonical data. Construct it in application assembly.
type Presenter struct {
	system    household.UnitSystem
	converter Converter
}

func NewPresenter(system household.UnitSystem, converter Converter) *Presenter {
	return &Presenter{system: system, converter: converter}
}

type fieldPresentation struct {
	metadata   QuantityMetadata
	conversion Affine
}

func (presenter *Presenter) field(
	field NumericField,
	support json.RawMessage,
	units ReadUnits,
) (fieldPresentation, error) {
	if _, err := ParseReadUnits(string(units)); err != nil {
		return fieldPresentation{}, err
	}
	quantity, err := ResolveQuantity(field, support)
	if err != nil {
		return fieldPresentation{}, err
	}
	meta := quantityFormat(quantity.Unit, field.ValueType)
	if units != UnitsCanonical {
		meta = presenter.displayFormat(quantity, field.ValueType)
	}
	conversion := Affine{Scale: 1}
	if meta.Unit != quantity.Unit {
		if presenter.converter == nil {
			return fieldPresentation{}, errors.New("quantity converter is not configured")
		}
		conversion, err = presenter.converter.Affine(quantity.Unit, meta.Unit)
		if err != nil {
			return fieldPresentation{}, err
		}
	}
	if _, err = conversion.Apply(0); err != nil {
		return fieldPresentation{}, err
	}
	if field.Range != nil {
		extent, rangeErr := ResolveRange(field, support)
		if errors.Is(rangeErr, ErrEmptyQuantityRange) {
			return fieldPresentation{metadata: meta, conversion: conversion}, nil
		}
		if rangeErr != nil {
			return fieldPresentation{}, rangeErr
		}
		minimum, minErr := conversion.Apply(extent.Minimum)
		maximum, maxErr := conversion.Apply(extent.Maximum)
		if minErr != nil || maxErr != nil {
			return fieldPresentation{}, errors.New("invalid projected quantity range")
		}
		meta.Range = &QuantityExtent{Minimum: minimum, Maximum: maximum}
	}
	return fieldPresentation{metadata: meta, conversion: conversion}, nil
}

func (presenter *Presenter) displayFormat(quantity ResolvedQuantity, valueType NumericValueType) QuantityMetadata {
	// Existing chromaticity presentation is a scale convention, not a
	// household preference or an inferred semantic kind.
	if quantity.Unit == unitChromaticity {
		//nolint:mnd // Scaled integer coordinates have four fractional digits.
		return QuantityMetadata{Unit: "1", Label: "", FractionDigits: 4}
	}
	if quantity.Kind == quantitykind.Temperature {
		if presenter.system == household.UnitSystemUS {
			return quantityFormat(unitFahrenheit, NumericNumber)
		}
		return quantityFormat(unitCelsius, NumericNumber)
	}
	if presenter.system == household.UnitSystemUS {
		switch quantity.Kind {
		case quantitykind.Speed:
			return quantityFormat(unitMilesPerHour, NumericNumber)
		case quantitykind.AtmosphericPressure:
			return quantityFormat(unitInchesMercury, NumericNumber)
		case quantitykind.PrecipitationDepth:
			return quantityFormat(unitInches, NumericNumber)
		case quantitykind.PrecipitationRate:
			return quantityFormat(unitInchesPerHour, NumericNumber)
		}
	}
	return quantityFormat(quantity.Unit, valueType)
}

func quantityFormat(unit string, valueType NumericValueType) QuantityMetadata {
	meta := QuantityMetadata{Unit: unit, Label: unit, FractionDigits: 1}
	if valueType == NumericInteger {
		meta.FractionDigits = 0
	}
	switch unit {
	case "1":
		meta.Label = ""
	case "/MK":
		meta.Label, meta.FractionDigits = "mired", 0
	case unitDegrees:
		meta.Label = "°"
	case unitCelsius:
		meta.Label = "°C"
	case unitFahrenheit:
		meta.Label = "°F"
	case unitMilesPerHour:
		meta.Label = "mi/h"
	case unitInchesMercury:
		meta.Label, meta.FractionDigits = "in\u00a0Hg", 2
	case unitInches:
		meta.Label, meta.FractionDigits = "in", 2
	case unitInchesPerHour:
		meta.Label, meta.FractionDigits = "in/h", 2
	}
	return meta
}

// Entity returns an isolated read representation; it never mutates canonical
// State, support, or evidence held by its caller. Optional numeric fields stay absent.
func (presenter *Presenter) Entity(
	view EntityWithState,
	units ReadUnits,
) (EntityWithState, map[string]QuantityMetadata, error) {
	if _, err := ParseReadUnits(string(units)); err != nil {
		return EntityWithState{}, nil, err
	}
	metadata := make(map[string]QuantityMetadata)
	if view.Entity.TypeID == EntityTypeHourlyforecastV1 || view.Entity.TypeID == EntityTypeDailyforecastV1 {
		return presenter.forecast(view, units)
	}
	for _, field := range BuiltinStateFields(view.Entity.TypeID) {
		numeric, ok := field.Value.(NumericField)
		if !ok {
			continue
		}
		presentation, err := presenter.field(numeric, json.RawMessage(view.Entity.Support), units)
		if err != nil {
			return EntityWithState{}, nil, fmt.Errorf("project field %q: %w", field.Path, err)
		}
		metadata[field.Path] = presentation.metadata
		if view.State != nil {
			value, valueErr := projectStateField(json.RawMessage(view.State.Value), field.Path, presentation)
			if valueErr != nil {
				return EntityWithState{}, nil, valueErr
			}
			state := *view.State
			state.Value = Value(value)
			view.State = &state
		}
	}
	return view, metadata, nil
}

func (presentation fieldPresentation) number(raw json.RawMessage) (json.RawMessage, error) {
	if string(raw) == "null" || (presentation.conversion.Scale == 1 && presentation.conversion.Offset == 0) {
		return raw, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return nil, err
	}
	value, err := number.Float64()
	if err != nil {
		return nil, err
	}
	converted, err := presentation.conversion.Apply(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func projectStateField(raw json.RawMessage, path string, presentation fieldPresentation) (json.RawMessage, error) {
	if presentation.conversion.Scale == 1 && presentation.conversion.Offset == 0 {
		return raw, nil
	}
	if path == "" {
		return presentation.number(raw)
	}
	// Catalogued scalar State paths are root properties, not arbitrary JSON paths.
	if !strings.HasPrefix(path, "/") || strings.Contains(path[1:], "/") {
		return nil, errors.New("unsupported State field path")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	key := path[1:]
	if value, exists := object[key]; exists {
		converted, err := presentation.number(value)
		if err != nil {
			return nil, err
		}
		object[key] = converted
		return json.Marshal(object)
	}
	return raw, nil
}
