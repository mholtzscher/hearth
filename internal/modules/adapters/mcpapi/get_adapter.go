package mcpapi

import (
	"context"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type GetAdapterInput struct {
	AdapterID adapterID `json:"adapter_id" doc:"subject-safe Adapter ID"`
}
type AdapterDetail struct {
	AdapterID string       `json:"adapter_id"`
	Health    HealthDetail `json:"health"`
}
type HealthDetail struct {
	HealthSummary

	Source           string           `json:"source"`
	Since            string           `json:"since"`
	EvidenceAt       string           `json:"evidence_at"`
	SourceObservedAt *string          `json:"source_observed_at,omitempty" nullable:"true"`
	Runtime          *RuntimeEvidence `json:"runtime,omitempty"`
}

//nolint:goconst // Keep the nullable JSON Schema branch beside its field.
func (HealthDetail) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	// Preserve the existing nullable runtime evidence, even though nil pointers
	// are normally omitted by this projection's encoder.
	schema.Properties["runtime"] = &huma.Schema{AnyOf: []*huma.Schema{
		{Type: "null"}, registry.Schema(reflect.TypeFor[RuntimeEvidence](), true, ""),
	}}
	return schema
}

type RuntimeEvidence struct {
	Status          string  `json:"status"`
	SoftwareName    string  `json:"software_name"`
	SoftwareVersion string  `json:"software_version"`
	LastHeartbeatAt *string `json:"last_heartbeat_at"`
	LeaseExpiresAt  string  `json:"lease_expires_at"`
}

func registerGetAdapter(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[GetAdapterInput, AdapterDetail]{
		Name:        "get_adapter",
		Description: "Inspect current Adapter health and runtime evidence, without private fencing identity.",
		Handler:     h.getAdapter,
	})
}

func (h *handler) getAdapter(ctx context.Context, in GetAdapterInput) (AdapterDetail, error) {
	item, err := h.adapters.GetAdapter(ctx, string(in.AdapterID))
	if err != nil {
		return AdapterDetail{}, managementFailure(err)
	}
	health := HealthDetail{HealthSummary: healthSummary(item.Health), Source: item.Health.Source,
		Since: formatTime(item.Health.Since), EvidenceAt: formatTime(item.Health.EvidenceAt)}
	if item.Health.SourceObservedAt != nil {
		value := formatTime(*item.Health.SourceObservedAt)
		health.SourceObservedAt = &value
	}
	if runtime := item.Health.Runtime; runtime != nil {
		health.Runtime = &RuntimeEvidence{Status: runtime.Status, SoftwareName: runtime.SoftwareName,
			SoftwareVersion: runtime.SoftwareVersion, LeaseExpiresAt: formatTime(runtime.LeaseExpiresAt)}
		if runtime.LastHeartbeatAt != nil {
			value := formatTime(*runtime.LastHeartbeatAt)
			health.Runtime.LastHeartbeatAt = &value
		}
	}
	return AdapterDetail{AdapterID: item.ID, Health: health}, nil
}
