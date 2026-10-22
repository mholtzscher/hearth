//nolint:goconst // Literal JSON fields keep terminal alternatives readable.
package httpapi

import (
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

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

// Schema describes complete alternatives, not independent optional evidence.
func (InvocationBody) Schema(_ huma.Registry) *huma.Schema {
	var variants []*huma.Schema
	for _, status := range []string{"running", "succeeded", "failed", "not_executed", "unknown"} {
		properties := map[string]*huma.Schema{
			"id":               {Type: "string"},
			"adapter_id":       {Type: "string"},
			"function_key":     {Type: "string"},
			"catalog_revision": {Type: "string"},
			"arguments":        {Type: "object"},
			"status":           {Type: "string", Enum: []any{status}},
			"started_at":       {Type: "string"},
			"deadline":         {Type: "string"},
			"completed_at":     {Type: "string"},
		}
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
			properties["result"] = &huma.Schema{Type: "object"}
			required = append(required, "result")
		default:
			properties["reason_code"] = &huma.Schema{Type: "string"}
			required = append(required, "reason_code")
		}
		variants = append(
			variants,
			&huma.Schema{Type: "object", AdditionalProperties: false, Properties: properties, Required: required},
		)
	}
	return &huma.Schema{Type: "object", OneOf: variants}
}
