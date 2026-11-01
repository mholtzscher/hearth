package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// inputDefaults implements the optional-properties traversal of the SDK's old
// applicator, not a JSON Schema interpreter. Required subtrees, refs, arrays,
// and composition branches are deliberately not traversed.
type inputDefaults struct {
	value      json.RawMessage
	properties map[string]inputDefaults
}

func prepareInputDefaults(compiler *jsonschema.Compiler, raw json.RawMessage, uri string) (inputDefaults, error) {
	var node struct {
		Default    json.RawMessage            `json:"default"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return inputDefaults{}, err
	}
	plan := inputDefaults{value: bytes.Clone(node.Default)}
	if node.Default != nil {
		validator, err := compiler.Compile(uri)
		if err != nil {
			return plan, err
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(node.Default))
		if err != nil {
			return plan, err
		}
		if err = validator.Validate(value); err != nil {
			return plan, fmt.Errorf("invalid schema default: %w", err)
		}
	}
	for name, property := range node.Properties {
		if slices.Contains(node.Required, name) {
			continue
		}
		child, err := prepareInputDefaults(compiler, property, uri+"/properties/"+url.PathEscape(escapePointer(name)))
		if err != nil {
			return plan, err
		}
		if child.value != nil || len(child.properties) > 0 {
			if plan.properties == nil {
				plan.properties = make(map[string]inputDefaults)
			}
			plan.properties[name] = child
		}
	}
	return plan, nil
}

func (plan inputDefaults) apply(raw json.RawMessage) (json.RawMessage, error) {
	if len(plan.properties) == 0 || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	changed := false
	for name, child := range plan.properties {
		value, present := fields[name]
		if !present {
			value = child.value
			if value == nil {
				value = json.RawMessage(`{}`)
			}
		}
		defaulted, err := child.apply(value)
		if err != nil {
			return nil, err
		}
		if !present || !bytes.Equal(value, defaulted) {
			fields[name] = defaulted
			changed = true
		}
	}
	if !changed {
		return raw, nil
	}
	// Append untouched raw child values directly. json.Marshal(fields) would
	// compact custom-decoded documents and change their raw byte-size checks.
	encoded := []byte{'{'}
	for index, name := range slices.Sorted(maps.Keys(fields)) {
		if index > 0 {
			encoded = append(encoded, ',')
		}
		key, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, key...)
		encoded = append(encoded, ':')
		encoded = append(encoded, fields[name]...)
	}
	return append(encoded, '}'), nil
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
