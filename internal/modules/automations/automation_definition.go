package automations

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//go:embed automation-definition.schema.json
var automationDefinitionSchema []byte

const automationDefinitionMaxBytes = 64 * 1024

// AutomationDefinitionCodec owns the compiled schema and explicit disabled default.
type AutomationDefinitionCodec struct{ schema *jsonschema.Schema }

// AutomationDefinitionIssue is a safe explanation at a JSON Pointer.
type AutomationDefinitionIssue struct {
	Path    string
	Message string
}

// AutomationDefinitionValidationError reports structural errors without payloads.
type AutomationDefinitionValidationError struct{ Issues []AutomationDefinitionIssue }

func (*AutomationDefinitionValidationError) Error() string {
	return "automation definition does not satisfy schema"
}

type automationTriggerJSON struct {
	ID         AutomationTriggerID `json:"id"`
	Kind       string              `json:"kind"`
	Expression string              `json:"expression"`
}
type automationStepJSON struct {
	EntityID      devices.EntityID      `json:"entity_id"`
	OperationName devices.OperationName `json:"operation_name"`
	Parameters    json.RawMessage       `json:"parameters"`
}
type automationDefinitionJSON struct {
	Name     string                  `json:"name"`
	Enabled  bool                    `json:"enabled"`
	Triggers []automationTriggerJSON `json:"triggers"`
	Steps    []automationStepJSON    `json:"steps"`
}

// NewAutomationDefinitionCodec compiles the canonical embedded schema at startup.
func NewAutomationDefinitionCodec() (*AutomationDefinitionCodec, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(automationDefinitionSchema))
	if err != nil {
		return nil, fmt.Errorf("automation schema decode: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const id = "urn:hearth:schema:automation-definition:v1"
	if err = compiler.AddResource(id, document); err != nil {
		return nil, fmt.Errorf("automation schema resource: %w", err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("automation schema compile: %w", err)
	}
	return &AutomationDefinitionCodec{schema: schema}, nil
}

// AutomationDefinitionSchema returns owned canonical schema bytes.
func (*AutomationDefinitionCodec) AutomationDefinitionSchema() json.RawMessage {
	return bytes.Clone(automationDefinitionSchema)
}

// DecodeAutomationDefinition validates before typed decoding can discard fields.
// Semantic catalog, identity, name, uniqueness and cron checks belong to the service.
func (codec *AutomationDefinitionCodec) DecodeAutomationDefinition(raw json.RawMessage) (AutomationDefinition, error) {
	var zero AutomationDefinition
	if len(raw) > automationDefinitionMaxBytes {
		return zero, definitionIssue("", "definition exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return zero, definitionIssue("", "expected one JSON document")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return zero, definitionIssue("", "expected one JSON document")
	}
	if err := codec.schema.Validate(document); err != nil {
		var validation *jsonschema.ValidationError
		if !errors.As(err, &validation) {
			return zero, definitionIssue("", "schema validation failed")
		}
		issues := []AutomationDefinitionIssue{}
		collectDefinitionIssues(validation, &issues)
		return zero, &AutomationDefinitionValidationError{Issues: issues}
	}
	var value automationDefinitionJSON
	if err := json.Unmarshal(raw, &value); err != nil {
		return zero, definitionIssue("", "invalid definition binding")
	}
	definition := AutomationDefinition{
		Name:     strings.TrimSpace(value.Name),
		Enabled:  value.Enabled,
		Triggers: make([]AutomationTrigger, len(value.Triggers)),
		Steps:    make([]AutomationStep, len(value.Steps)),
	}
	for i, t := range value.Triggers {
		definition.Triggers[i] = AutomationTrigger{
			ID:         t.ID,
			Kind:       t.Kind,
			Expression: strings.Join(strings.Fields(t.Expression), " "),
		}
	}
	for i, s := range value.Steps {
		definition.Steps[i] = AutomationStep{
			EntityID:      s.EntityID,
			OperationName: s.OperationName,
			Parameters:    devices.CommandParameters(bytes.Clone(s.Parameters)),
		}
	}
	return definition, nil
}

// ValidateAutomationDefinition applies the same structural schema to domain callers.
func (codec *AutomationDefinitionCodec) ValidateAutomationDefinition(definition AutomationDefinition) error {
	raw, err := encodeAutomationDefinition(definition)
	if err != nil {
		return definitionIssue("/steps", "invalid parameter JSON")
	}
	_, err = codec.DecodeAutomationDefinition(raw)
	return err
}
func definitionIssue(path, message string) error {
	return &AutomationDefinitionValidationError{Issues: []AutomationDefinitionIssue{{Path: path, Message: message}}}
}
func collectDefinitionIssues(validation *jsonschema.ValidationError, issues *[]AutomationDefinitionIssue) {
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
	*issues = append(
		*issues,
		AutomationDefinitionIssue{Path: pointer.String(), Message: "value does not satisfy schema"},
	)
}
func encodeAutomationDefinition(definition AutomationDefinition) ([]byte, error) {
	value := automationDefinitionJSON{Name: definition.Name, Enabled: definition.Enabled}
	if definition.Triggers != nil {
		value.Triggers = make([]automationTriggerJSON, len(definition.Triggers))
	}
	if definition.Steps != nil {
		value.Steps = make([]automationStepJSON, len(definition.Steps))
	}
	for i, t := range definition.Triggers {
		value.Triggers[i] = automationTriggerJSON(t)
	}
	for i, s := range definition.Steps {
		value.Steps[i] = automationStepJSON{
			EntityID:      s.EntityID,
			OperationName: s.OperationName,
			Parameters:    json.RawMessage(s.Parameters),
		}
	}
	return json.Marshal(value)
}

type automationRunSnapshotJSON struct {
	AutomationID AutomationID    `json:"automation_id"`
	Revision     int64           `json:"revision"`
	Definition   json.RawMessage `json:"definition"`
	Timezone     string          `json:"timezone"`
}

func encodeAutomationRunSnapshot(snapshot AutomationRunSnapshot) ([]byte, error) {
	definition, err := encodeAutomationDefinition(snapshot.Definition)
	if err != nil {
		return nil, err
	}
	return json.Marshal(
		automationRunSnapshotJSON{
			AutomationID: snapshot.AutomationID,
			Revision:     snapshot.Revision,
			Definition:   definition,
			Timezone:     snapshot.Timezone,
		},
	)
}
func decodeAutomationRunSnapshot(raw []byte) (AutomationRunSnapshot, error) {
	var value automationRunSnapshotJSON
	var snapshot AutomationRunSnapshot
	if err := json.Unmarshal(raw, &value); err != nil {
		return snapshot, err
	}
	var definition automationDefinitionJSON
	if err := json.Unmarshal(value.Definition, &definition); err != nil {
		return snapshot, err
	}
	snapshot = AutomationRunSnapshot{
		AutomationID: value.AutomationID,
		Revision:     value.Revision,
		Timezone:     value.Timezone,
		Definition: AutomationDefinition{
			Name:     definition.Name,
			Enabled:  definition.Enabled,
			Triggers: make([]AutomationTrigger, len(definition.Triggers)),
			Steps:    make([]AutomationStep, len(definition.Steps)),
		},
	}
	for i, trigger := range definition.Triggers {
		snapshot.Definition.Triggers[i] = AutomationTrigger(trigger)
	}
	for i, step := range definition.Steps {
		snapshot.Definition.Steps[i] = AutomationStep{
			EntityID:      step.EntityID,
			OperationName: step.OperationName,
			Parameters:    devices.CommandParameters(step.Parameters),
		}
	}
	return snapshot, nil
}
