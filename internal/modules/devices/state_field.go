package devices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mholtzscher/hearth/entitytypes/quantitykind"
)

// ErrEntityInterpretationImmutable rejects changes that would reinterpret retained State.
var ErrEntityInterpretationImmutable = errors.New("entity interpretation is immutable")

var supportReferencePattern = regexp.MustCompile(`^(/[a-z][a-z0-9_]*)+$`)

// StateField is a scalar projection within an existing State, not an observation.
type StateField struct {
	Path     string
	Presence FieldPresence
	Value    StateFieldValue
}

type FieldPresence string

const (
	FieldAlways    FieldPresence = "always"
	FieldSometimes FieldPresence = "sometimes"
)

// StateFieldValue is closed to the supported scalar alternatives.
type StateFieldValue interface{ stateFieldValue() }

type NumericValueType string

const (
	NumericInteger NumericValueType = "integer"
	NumericNumber  NumericValueType = "number"
)

type NumericField struct {
	ValueType NumericValueType
	Unit      UnitSource
	Kind      KindSource
	// Range is the field's meaningful extent in its canonical unit, when the
	// type declares one. It describes presentation, not admissible State.
	Range *QuantityRange
}

type BooleanField struct{}
type StringField struct{ Enum []string }

func (NumericField) stateFieldValue() {}
func (BooleanField) stateFieldValue() {}
func (StringField) stateFieldValue()  {}

type UnitSource interface{ unitSource() }
type FixedUnit struct{ Code string }
type SupportUnit struct {
	Reference SupportReference
	Optional  bool
}
type SupportReference struct{ Path string }

func (FixedUnit) unitSource()   {}
func (SupportUnit) unitSource() {}

// KindSource identifies a fixed quantity meaning or a support declaration.
// A nil source means the field is unclassified.
type KindSource interface{ kindSource() }
type FixedKind struct{ Code string }
type SupportKind struct {
	Reference SupportReference
	Optional  bool
}

func (FixedKind) kindSource()   {}
func (SupportKind) kindSource() {}

type QuantityRange struct {
	Minimum RangeBound
	Maximum RangeBound
}

// RangeBound is closed to a literal value or a required numeric support property.
type RangeBound interface{ rangeBound() }
type LiteralBound struct{ Value float64 }
type SupportBound struct{ Reference SupportReference }

func (LiteralBound) rangeBound() {}
func (SupportBound) rangeBound() {}

// BuiltinStateField returns an isolated copy of the schema-derived descriptor.
func BuiltinStateField(id EntityTypeID, path string) (StateField, bool) {
	for _, field := range builtinStateFields[id] {
		if field.Path == path {
			return copyStateField(field), true
		}
	}
	return StateField{}, false
}

func BuiltinStateFields(id EntityTypeID) []StateField {
	fields := builtinStateFields[id]
	result := make([]StateField, len(fields))
	for index, field := range fields {
		result[index] = copyStateField(field)
	}
	return result
}

func copyStateField(field StateField) StateField {
	switch value := field.Value.(type) {
	case StringField:
		value.Enum = append([]string(nil), value.Enum...)
		field.Value = value
	case NumericField:
		if value.Range != nil {
			bounds := *value.Range
			value.Range = &bounds
			field.Value = value
		}
	}
	return field
}

type ResolvedQuantity struct {
	Unit string
	Kind string
}

// ResolveQuantity resolves canonical interpretation; household conversion belongs
// to presentation. It rejects invalid required support rather than inventing it.
func ResolveQuantity(field NumericField, support json.RawMessage) (ResolvedQuantity, error) {
	if field.ValueType != NumericInteger && field.ValueType != NumericNumber {
		return ResolvedQuantity{}, errors.New("invalid numeric value type")
	}
	unit, err := resolveUnit(field.Unit, support)
	if err != nil {
		return ResolvedQuantity{}, err
	}
	var kind string
	switch source := field.Kind.(type) {
	case nil:
	case FixedKind:
		kind = source.Code
		if kind == "" {
			return ResolvedQuantity{}, errors.New("fixed kind is empty")
		}
	case SupportKind:
		value, present, referenceErr := supportReferenceValue(support, source.Reference.Path)
		if referenceErr != nil {
			return ResolvedQuantity{}, referenceErr
		}
		if !present && !source.Optional {
			return ResolvedQuantity{}, fmt.Errorf("required kind %q is absent", source.Reference.Path)
		}
		if present {
			var ok bool
			kind, ok = value.(string)
			if !ok || kind == "" {
				return ResolvedQuantity{}, fmt.Errorf("kind %q must be a nonempty string", source.Reference.Path)
			}
		}
	default:
		return ResolvedQuantity{}, errors.New("invalid kind source")
	}
	if kind != "" {
		if validationErr := quantitykind.Validate(kind, unit); validationErr != nil {
			return ResolvedQuantity{}, validationErr
		}
	}
	return ResolvedQuantity{Unit: unit, Kind: kind}, nil
}

func resolveUnit(source UnitSource, support json.RawMessage) (string, error) {
	switch unit := source.(type) {
	case FixedUnit:
		if unit.Code == "" {
			return "", errors.New("fixed unit is empty")
		}
		return unit.Code, nil
	case SupportUnit:
		value, present, err := supportReferenceValue(support, unit.Reference.Path)
		if err != nil {
			return "", err
		}
		if !present && !unit.Optional {
			return "", fmt.Errorf("required unit %q is absent", unit.Reference.Path)
		}
		if !present {
			return "1", nil
		}
		text, ok := value.(string)
		if !ok || text == "" {
			return "", fmt.Errorf("unit %q must be a nonempty string", unit.Reference.Path)
		}
		return text, nil
	default:
		return "", errors.New("invalid unit source")
	}
}

type ResolvedRange struct {
	Minimum float64
	Maximum float64
}

// ErrNoQuantityRange reports a field whose type declares no range.
var ErrNoQuantityRange = errors.New("quantity has no declared range")

// ErrEmptyQuantityRange identifies valid support that declares a single value,
// not a usable presentation extent. Reads may omit that extent; Range cards
// still require a strictly increasing range.
var ErrEmptyQuantityRange = errors.New("range minimum equals its maximum")

// ResolveRange resolves a field's declared range against an Entity's support,
// in the field's canonical unit. Support that cannot bound the field is an error.
func ResolveRange(field NumericField, support json.RawMessage) (ResolvedRange, error) {
	if field.Range == nil {
		return ResolvedRange{}, ErrNoQuantityRange
	}
	minimum, err := resolveBound(field.Range.Minimum, support)
	if err != nil {
		return ResolvedRange{}, err
	}
	maximum, err := resolveBound(field.Range.Maximum, support)
	if err != nil {
		return ResolvedRange{}, err
	}
	if minimum == maximum {
		return ResolvedRange{}, ErrEmptyQuantityRange
	}
	if !(minimum < maximum) {
		return ResolvedRange{}, errors.New("range minimum must be below its maximum")
	}
	return ResolvedRange{Minimum: minimum, Maximum: maximum}, nil
}

func resolveBound(bound RangeBound, support json.RawMessage) (float64, error) {
	switch bound := bound.(type) {
	case LiteralBound:
		return bound.Value, nil
	case SupportBound:
		value, present, err := supportReferenceValue(support, bound.Reference.Path)
		if err != nil {
			return 0, err
		}
		number, ok := value.(json.Number)
		if !present || !ok {
			return 0, fmt.Errorf("range bound %q must be a number", bound.Reference.Path)
		}
		return number.Float64()
	default:
		return 0, errors.New("invalid range bound")
	}
}

type interpretationReference struct {
	Path         string
	Optional     bool
	AllowInitial bool
}

// ValidateInterpretationUnchanged compares only generated interpretation paths.
// Unit absence is frozen independently of its effective dimensionless unit.
// Transition errors take precedence over incompatible incoming quantity pairs.
//
//nolint:gocognit // Each transition is checked explicitly for generated references.
func ValidateInterpretationUnchanged(id EntityTypeID, previous, next json.RawMessage) error {
	for _, reference := range builtinInterpretationReferences[id] {
		before, beforePresent, err := supportReferenceValue(previous, reference.Path)
		if err != nil {
			return fmt.Errorf("previous interpretation %q: %w", reference.Path, err)
		}
		after, afterPresent, err := supportReferenceValue(next, reference.Path)
		if err != nil {
			return fmt.Errorf("next interpretation %q: %w", reference.Path, err)
		}
		if (!beforePresent || !afterPresent) && !reference.Optional {
			return fmt.Errorf("required interpretation %q is absent", reference.Path)
		}
		if beforePresent != afterPresent && (!reference.AllowInitial || beforePresent || !afterPresent) {
			return fmt.Errorf("%w: %s", ErrEntityInterpretationImmutable, reference.Path)
		}
		if !beforePresent && afterPresent {
			if _, valueErr := equalInterpretation(reference, after, after); valueErr != nil {
				return valueErr
			}
			continue
		}
		if !beforePresent {
			continue
		}
		equal, err := equalInterpretation(reference, before, after)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("%w: %s", ErrEntityInterpretationImmutable, reference.Path)
		}
	}
	for _, field := range builtinStateFields[id] {
		numeric, ok := field.Value.(NumericField)
		if !ok {
			continue
		}
		if _, err := ResolveQuantity(numeric, next); err != nil {
			return fmt.Errorf("next quantity %q: %w", field.Path, err)
		}
	}
	return nil
}

func equalInterpretation(reference interpretationReference, before, after any) (bool, error) {
	left, leftOK := before.(string)
	right, rightOK := after.(string)
	if !leftOK || !rightOK || left == "" || right == "" {
		return false, fmt.Errorf("interpretation %q must be a nonempty string", reference.Path)
	}
	return left == right, nil
}

func supportReferenceValue(raw json.RawMessage, path string) (any, bool, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false, fmt.Errorf("decode support: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, false, errors.New("support must contain one JSON value")
	}
	if _, object := value.(map[string]any); !object {
		return nil, false, errors.New("support must be an object")
	}
	if !supportReferencePattern.MatchString(path) || len(path) > 64 {
		return nil, false, errors.New("invalid support reference path")
	}
	for token := range strings.SplitSeq(path[1:], "/") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("support reference %q traverses a non-object", path)
		}
		value, ok = object[token]
		if !ok {
			return nil, false, nil
		}
	}
	return value, true, nil
}
