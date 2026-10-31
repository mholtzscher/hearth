package mcpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const inputSchemaURI = "https://hearth.invalid/mcp-input.json"

// EnvelopeValidator checks original envelope constraints before decoding. It
// must work on a zero-valued input and must not decode domain values or act.
type EnvelopeValidator interface {
	ValidateEnvelope(json.RawMessage) error
}

type preparedInput[I any] struct {
	advertised json.RawMessage
	validator  *jsonschema.Schema
	defaults   inputDefaults
}

func prepareInput[I any]() (*preparedInput[I], error) {
	if reflect.TypeFor[I]().Kind() == reflect.Interface {
		return nil, fmt.Errorf("tool input requires a concrete type")
	}
	raw, err := prepareInputSchema[I]()
	if err != nil {
		return nil, err
	}
	compiler, validator, err := compileObjectContract(inputSchemaURI, raw)
	if err != nil {
		return nil, fmt.Errorf("prepare input schema: %w", err)
	}
	defaults, err := prepareInputDefaults(compiler, raw, inputSchemaURI+"#")
	if err != nil {
		return nil, fmt.Errorf("prepare input defaults: %w", err)
	}
	return &preparedInput[I]{advertised: raw, validator: validator, defaults: defaults}, nil
}

func (input *preparedInput[I]) Decode(raw json.RawMessage) (I, error) {
	var value I
	// Pointer inputs have the same zero-valued receiver convention as values.
	if typ := reflect.TypeFor[I](); typ.Kind() == reflect.Pointer {
		reflect.ValueOf(&value).Elem().Set(reflect.New(typ.Elem()))
	}
	if err := validateEnvelope(&value, raw); err != nil {
		return value, err
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if !raw.IsValid() {
		return value, invalidArguments("invalid_json")
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return value, invalidArguments("type_mismatch")
	}
	defaulted, err := input.defaults.apply(raw)
	if err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(defaulted))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		return value, input.bindingFailure(defaulted, err)
	}
	return value, input.validate(defaulted)
}

func (input *preparedInput[I]) validate(raw json.RawMessage) error {
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if err = input.validator.Validate(instance); err != nil {
		return inputSchemaFailure(input.validator, instance, err)
	}
	return nil
}

func validateEnvelope[I any](value *I, raw json.RawMessage) error {
	if validator, ok := any(value).(EnvelopeValidator); ok {
		return validator.ValidateEnvelope(raw)
	}
	if validator, ok := any(*value).(EnvelopeValidator); ok {
		return validator.ValidateEnvelope(raw)
	}
	return nil
}

func (input *preparedInput[I]) bindingFailure(raw json.RawMessage, err error) error {
	if _, ok := errors.AsType[*ToolError](err); ok {
		return err
	}
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		// Schema diagnostics identify declared fields without publishing Go's
		// parser text or submitted values. Module-owned errors above still win.
		if schemaErr := input.validate(raw); schemaErr != nil {
			return schemaErr
		}
		return invalidArguments("type_mismatch")
	}
	if _, ok := errors.AsType[*json.SyntaxError](err); ok {
		return invalidArguments("invalid_json")
	}
	// Custom decoder failures have to be modeled by their owner. An arbitrary
	// error can describe a server invariant and must not be blamed on input.
	return err
}
