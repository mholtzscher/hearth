package devices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
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
	if value, ok := field.Value.(StringField); ok {
		value.Enum = append([]string(nil), value.Enum...)
		field.Value = value
	}
	return field
}

type ResolvedQuantity struct {
	Unit string
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
	return ResolvedQuantity{Unit: unit}, nil
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

type interpretationReference struct {
	Path     string
	Optional bool
}

// ValidateInterpretationUnchanged compares only generated interpretation paths.
// Optional absence is frozen independently of its effective dimensionless unit.
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
		if beforePresent != afterPresent {
			return fmt.Errorf("%w: %s", ErrEntityInterpretationImmutable, reference.Path)
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
