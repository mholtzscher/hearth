//nolint:goconst // Literal JSON fields keep the public schema alternatives readable.
package api

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

const maximumFunctionBodyBytes = 128 * 1024

type FunctionBody struct {
	AdapterID       string          `json:"adapter_id"`
	FunctionKey     string          `json:"function_key"`
	CatalogRevision string          `json:"catalog_revision"`
	Description     string          `json:"description"`
	InputSchema     json.RawMessage `json:"input_schema"`
	OutputSchema    json.RawMessage `json:"output_schema"`
	TimeoutMS       int             `json:"timeout_ms"`
	Callable        bool            `json:"callable"`
}

type InvokeBody struct {
	CatalogRevision string          `json:"catalog_revision"`
	Arguments       json.RawMessage `json:"arguments"`
}

// UnmarshalJSON validates only the outer contract. The service owns argument
// normalization, bounds, and dynamic schema validation.
func (body *InvokeBody) UnmarshalJSON(data []byte) error {
	if !jsontext.Value(data).IsValid() {
		return errors.New("invalid function input")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 2 {
		return errors.New("invalid function input")
	}
	var decoded InvokeBody
	revision, present := fields["catalog_revision"]
	if !present || bytes.Equal(revision, []byte("null")) || json.Unmarshal(revision, &decoded.CatalogRevision) != nil {
		return errors.New("invalid function input")
	}
	arguments, present := fields["arguments"]
	if !present || len(bytes.TrimSpace(arguments)) == 0 || bytes.TrimSpace(arguments)[0] != '{' {
		return errors.New("invalid function input")
	}
	decoded.Arguments = arguments
	*body = decoded
	return nil
}

type InvocationBody struct {
	ID              string          `json:"id"`
	AdapterID       string          `json:"adapter_id"`
	FunctionKey     string          `json:"function_key"`
	CatalogRevision string          `json:"catalog_revision"`
	Arguments       json.RawMessage `json:"arguments"`
	Status          string          `json:"status"`
	Result          json.RawMessage `json:"result,omitempty"`
	ReasonCode      string          `json:"reason_code,omitempty"`
	StartedAt       string          `json:"started_at"`
	Deadline        string          `json:"deadline"`
	CompletedAt     *string         `json:"completed_at"`
}

type FunctionCollectionBody struct {
	Items      []FunctionBody `json:"items"`
	NextCursor *string        `json:"next_cursor,omitempty"`
}

type InvocationCollectionBody struct {
	Items      []InvocationBody `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

func (FunctionCollectionBody) Schema(_ huma.Registry) *huma.Schema {
	return functionCollectionSchema(FunctionBody{}.Schema(nil))
}

func (InvocationCollectionBody) Schema(_ huma.Registry) *huma.Schema {
	return functionCollectionSchema(InvocationBody{}.Schema(nil))
}

func functionCollectionSchema(items *huma.Schema) *huma.Schema {
	return &huma.Schema{Type: "object", AdditionalProperties: false, Required: []string{"items"},
		Properties: map[string]*huma.Schema{"items": {Type: "array", Items: items}, "next_cursor": textSchema()}}
}

func functionBody(function adapters.Function) FunctionBody {
	return FunctionBody{AdapterID: function.AdapterID, FunctionKey: string(function.Descriptor.Key),
		CatalogRevision: string(function.CatalogRevision), Description: function.Descriptor.Description,
		InputSchema: function.Descriptor.InputSchema, OutputSchema: function.Descriptor.OutputSchema,
		TimeoutMS: function.Descriptor.TimeoutMS, Callable: function.Callable}
}

func invocationBody(invocation adapters.Invocation) InvocationBody {
	body := InvocationBody{ID: string(invocation.ID), AdapterID: invocation.Route.AdapterID,
		FunctionKey: string(invocation.Function.Key), CatalogRevision: string(invocation.CatalogRevision),
		Arguments: invocation.Arguments, Status: "running", StartedAt: formatTime(invocation.StartedAt),
		Deadline: formatTime(invocation.Deadline)}
	if invocation.Completion == nil {
		return body
	}
	completed := formatTime(invocation.Completion.At)
	body.CompletedAt = &completed
	switch evidence := invocation.Completion.Evidence.(type) {
	case adapters.Succeeded:
		body.Status, body.Result = "succeeded", evidence.Result
	case adapters.Failed:
		body.Status, body.ReasonCode = "failed", evidence.ReasonCode
	case adapters.NotExecuted:
		body.Status, body.ReasonCode = "not_executed", evidence.ReasonCode
	case adapters.Unknown:
		body.Status, body.ReasonCode = "unknown", evidence.ReasonCode
	}
	return body
}

func functionNextCursor(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func objectSchema() *huma.Schema { return &huma.Schema{Type: "object"} }
func textSchema() *huma.Schema   { return &huma.Schema{Type: "string"} }

func (FunctionBody) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required: []string{
			"adapter_id",
			"function_key",
			"catalog_revision",
			"description",
			"input_schema",
			"output_schema",
			"timeout_ms",
			"callable",
		},
		Properties: map[string]*huma.Schema{"adapter_id": textSchema(), "function_key": textSchema(),
			"catalog_revision": textSchema(), "description": textSchema(), "input_schema": objectSchema(),
			"output_schema": objectSchema(), "timeout_ms": {Type: "integer"}, "callable": {Type: "boolean"}},
	}
}

func (InvokeBody) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "object", AdditionalProperties: false,
		Required:   []string{"catalog_revision", "arguments"},
		Properties: map[string]*huma.Schema{"catalog_revision": textSchema(), "arguments": objectSchema()}}
}

// Schema describes complete alternatives, not independent optional evidence.
func (InvocationBody) Schema(_ huma.Registry) *huma.Schema {
	var variants []*huma.Schema
	for _, status := range []string{"running", "succeeded", "failed", "not_executed", "unknown"} {
		properties := map[string]*huma.Schema{"id": textSchema(), "adapter_id": textSchema(),
			"function_key": textSchema(), "catalog_revision": textSchema(), "arguments": objectSchema(),
			"status": {Type: "string", Enum: []any{status}}, "started_at": textSchema(), "deadline": textSchema(),
			"completed_at": textSchema()}
		required := []string{
			"id",
			"adapter_id",
			"function_key",
			"catalog_revision",
			"arguments",
			"status",
			"started_at",
			"deadline",
			"completed_at",
		}
		switch status {
		case "running":
			properties["completed_at"] = &huma.Schema{Type: "null"}
		case "succeeded":
			properties["result"] = objectSchema()
			required = append(required, "result")
		default:
			properties["reason_code"] = textSchema()
			required = append(required, "reason_code")
		}
		variants = append(
			variants,
			&huma.Schema{Type: "object", AdditionalProperties: false, Properties: properties, Required: required},
		)
	}
	return &huma.Schema{Type: "object", OneOf: variants}
}
