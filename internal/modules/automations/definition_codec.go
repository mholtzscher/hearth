package automations

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed automation-definition.schema.json
var automationDefinitionSchema []byte

// automationDefinitionCodec compiles the embedded strict persisted shape once per process.
//
//nolint:gochecknoglobals // One immutable compiled schema, never reassigned.
var automationDefinitionCodec = sync.OnceValues(NewDefinitionCodec)

// DefinitionCodec owns the compiled strict definition schema.
type DefinitionCodec struct {
	schema     *jsonschema.Schema
	triggers   *jsonschema.Schema
	conditions *jsonschema.Schema
}

// NewDefinitionCodec compiles the canonical embedded schema.
func NewDefinitionCodec() (*DefinitionCodec, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(automationDefinitionSchema))
	if err != nil {
		return nil, fmt.Errorf("automation definition schema decode: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaID = "urn:hearth:schema:automation-definition:v2"
	if err = compiler.AddResource(schemaID, document); err != nil {
		return nil, fmt.Errorf("automation definition schema resource: %w", err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return nil, fmt.Errorf("automation definition schema compile: %w", err)
	}
	triggers, err := compiler.Compile(schemaID + "#/properties/triggers/items")
	if err != nil {
		return nil, fmt.Errorf("automation trigger schema compile: %w", err)
	}
	conditions, err := compiler.Compile(schemaID + "#/$defs/condition")
	if err != nil {
		return nil, fmt.Errorf("automation condition schema compile: %w", err)
	}
	return &DefinitionCodec{schema: compiled, triggers: triggers, conditions: conditions}, nil
}

// AutomationDefinitionSchema returns owned copies of the embedded strict shape.
func (*DefinitionCodec) AutomationDefinitionSchema() json.RawMessage {
	return bytes.Clone(automationDefinitionSchema)
}

// DecodeDefinition validates and normalizes JSON against the strict schema and structural rules.
func DecodeDefinition(raw json.RawMessage) (Definition, error) {
	if len(raw) == 0 {
		return Definition{}, definitionIssue("", "definition is required")
	}
	if len(raw) > automationDefinitionMaxBytes {
		return Definition{}, definitionIssue(
			"", fmt.Sprintf("definition exceeds %d bytes", automationDefinitionMaxBytes),
		)
	}
	codec, err := automationDefinitionCodec()
	if err != nil {
		return Definition{}, err
	}
	document, err := decodeJSONValue(raw)
	if err != nil {
		return Definition{}, definitionIssue("", "definition must be exactly one JSON object")
	}
	if err = boundDefinitionJSON(document); err != nil {
		return Definition{}, err
	}
	if err = codec.schema.Validate(document); err != nil {
		var validation *jsonschema.ValidationError
		if !errors.As(err, &validation) {
			return Definition{}, definitionIssue("", "definition does not satisfy the strict schema")
		}
		issues := make([]DefinitionIssue, 0, len(validation.Causes)+1)
		collectDefinitionIssues(validation, &issues)
		return Definition{}, &DefinitionError{Issues: issues}
	}
	var value automationDefinitionJSON
	if err = json.Unmarshal(raw, &value); err != nil {
		return Definition{}, definitionIssue("", "definition cannot be bound")
	}
	// Check normalized bytes as well as the input size.
	normalized, _, err := NormalizeAndEncodeDefinition(automationDefinitionFromJSON(value))
	return normalized, err
}

// boundDefinitionJSON rejects excessive recursive nesting before schema evaluation
// and recursive typed conversion. Shape checks remain the schema's responsibility.
func boundDefinitionJSON(document any) error {
	object, _ := document.(map[string]any)
	if err := boundConditionJSON(object["conditions"], 1, new(int)); err != nil {
		return err
	}
	return boundStepSequenceJSON(object["steps"], 1, new(int))
}

func boundConditionJSON(value any, depth int, count *int) error {
	if value == nil {
		return nil
	}
	*count++
	if depth > automationConditionMaxDepth || *count > automationConditionMaxNodes {
		return definitionIssue("/conditions", "Condition tree exceeds its depth or node bound")
	}
	object, _ := value.(map[string]any)
	children, _ := object["children"].([]any)
	for _, child := range children {
		if err := boundConditionJSON(child, depth+1, count); err != nil {
			return err
		}
	}
	return boundConditionJSON(object["child"], depth+1, count)
}

func boundStepSequenceJSON(value any, depth int, count *int) error {
	steps, _ := value.([]any)
	for _, value := range steps {
		*count++
		if depth > automationStepMaxDepth || *count > automationAllStepsMaxCount {
			return definitionIssue("/steps", "Step tree exceeds its depth or node bound")
		}
		object, _ := value.(map[string]any)
		if err := boundConditionJSON(object["conditions"], 1, new(int)); err != nil {
			return err
		}
		for _, field := range []string{"then", "else", "default"} {
			if err := boundStepSequenceJSON(object[field], depth+1, count); err != nil {
				return err
			}
		}
		branches, _ := object["branches"].([]any)
		if err := boundChooseBranchesJSON(branches, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

func boundChooseBranchesJSON(branches []any, depth int, count *int) error {
	for _, value := range branches {
		branch, _ := value.(map[string]any)
		if err := boundConditionJSON(branch["conditions"], 1, new(int)); err != nil {
			return err
		}
		if err := boundStepSequenceJSON(branch["steps"], depth, count); err != nil {
			return err
		}
	}
	return nil
}

// EncodeDefinition validates arbitrary domain input before recursive encoding.
// Recursive depth/count bounds also
// terminate cyclic Go values before copying or encoding them.
func EncodeDefinition(definition Definition) (json.RawMessage, error) {
	normalized, err := prepareDefinition(definition)
	if err != nil {
		return nil, err
	}
	return encodePreparedDefinition(normalized)
}

// encodePreparedDefinition consumes an unchanged, structurally normalized value.
func encodePreparedDefinition(definition Definition) (json.RawMessage, error) {
	value := automationDefinitionJSON{
		Name:       definition.Name,
		Enabled:    definition.Enabled,
		Triggers:   make([]automationTriggerJSON, 0, len(definition.Triggers)),
		Conditions: encodeAutomationConditionTree(definition.Conditions),
		Steps:      make([]automationStepJSON, 0, len(definition.Steps)),
	}
	for _, trigger := range definition.Triggers {
		value.Triggers = append(value.Triggers, encodeAutomationTrigger(trigger))
	}
	for _, step := range definition.Steps {
		value.Steps = append(value.Steps, encodeAutomationStep(step))
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: definition cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	if len(raw) > automationDefinitionMaxBytes {
		return nil, definitionIssue("", "definition exceeds 65536 bytes")
	}
	return raw, nil
}

// EncodeMatchedTriggers renders matching Trigger snapshots in the same strict
// persisted shape as a definition's Triggers.
func EncodeMatchedTriggers(triggers []Trigger) (json.RawMessage, error) {
	encoded := make([]automationTriggerJSON, 0, len(triggers))
	for _, trigger := range triggers {
		normalized, err := normalizeAutomationTriggerValue(trigger)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, encodeAutomationTrigger(normalized))
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: matched triggers cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

// DecodeMatchedTriggers validates and normalizes a persisted Trigger snapshot
// list back into domain values.
func DecodeMatchedTriggers(raw json.RawMessage) ([]Trigger, error) {
	codec, err := automationDefinitionCodec()
	if err != nil {
		return nil, err
	}
	document, err := decodeJSONValue(raw)
	if err != nil {
		return nil, invalid("matched triggers must contain exactly one JSON array")
	}
	items, ok := document.([]any)
	if !ok || len(items) > automationTriggerMaxCount {
		return nil, invalid("matched triggers must be an array of at most 32 triggers")
	}
	for _, item := range items {
		if err = codec.triggers.Validate(item); err != nil {
			return nil, invalid("matched triggers do not satisfy the strict trigger schema")
		}
	}
	var encoded []automationTriggerJSON
	if err = json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	triggers := make([]Trigger, 0, len(encoded))
	for _, item := range encoded {
		trigger, normalizeErr := normalizeAutomationTriggerValue(automationTriggerFromJSON(item))
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		triggers = append(triggers, trigger)
	}
	return triggers, nil
}

type automationDefinitionJSON struct {
	Name       string                   `json:"name"`
	Enabled    bool                     `json:"enabled"`
	Triggers   []automationTriggerJSON  `json:"triggers"`
	Conditions *automationConditionJSON `json:"conditions,omitempty"`
	Steps      []automationStepJSON     `json:"steps"`
}

// automationDefinitionFromJSON maps a schema-validated document to domain types.
func automationDefinitionFromJSON(value automationDefinitionJSON) Definition {
	definition := Definition{
		Name:       value.Name,
		Enabled:    value.Enabled,
		Triggers:   make([]Trigger, 0, len(value.Triggers)),
		Conditions: decodeConditionTree(value.Conditions),
		Steps:      decodeAutomationSteps(value.Steps),
	}
	for _, item := range value.Triggers {
		definition.Triggers = append(definition.Triggers, automationTriggerFromJSON(item))
	}
	return definition
}

func collectDefinitionIssues(validation *jsonschema.ValidationError, issues *[]DefinitionIssue) {
	if len(validation.Causes) > 0 {
		for _, cause := range validation.Causes {
			collectDefinitionIssues(cause, issues)
		}
		return
	}
	var pointer strings.Builder
	for _, part := range validation.InstanceLocation {
		pointer.WriteString("/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1"))
	}
	*issues = append(*issues, DefinitionIssue{
		Path:    pointer.String(),
		Message: "value does not satisfy the strict schema",
	})
}
