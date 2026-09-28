package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// StartAutomationRunBody carries only the optional Condition bypass request for
// a manual Run; an omitted body applies Conditions like an explicit false.
type StartAutomationRunBody struct {
	BypassConditions bool `json:"bypass_conditions,omitempty"`
}

// UnmarshalJSON enforces the strict optional manual bypass body, closing the
// shapes the generated object schema accepts but the product contract forbids.
func (body *StartAutomationRunBody) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var members map[string]json.RawMessage
	if err := decoder.Decode(&members); err != nil {
		return errors.New("manual run body must be one JSON object")
	}
	if members == nil {
		return errors.New("manual run body must be a JSON object, not null")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("manual run body must contain exactly one JSON object")
	}
	for member := range members {
		if member != "bypass_conditions" {
			return errors.New("manual run body has an unknown member")
		}
	}
	bypass := false
	if operand, present := members["bypass_conditions"]; present {
		switch strings.TrimSpace(string(operand)) {
		case "true":
			bypass = true
		case "false":
			bypass = false
		default:
			return errors.New("bypass_conditions must be a JSON boolean")
		}
	}
	// Assign only after every member is validated, so a rejected body never
	// leaves a partially-applied bypass behind.
	*body = StartAutomationRunBody{BypassConditions: bypass}
	return nil
}
