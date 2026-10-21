package httpapi

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func adapterBody(instance adapters.AdapterInstance) AdapterBody {
	health := AdapterHealthBody{
		Status: string(instance.Health.Status), Source: instance.Health.Source,
		Since: formatTime(instance.Health.Since), EvidenceAt: formatTime(instance.Health.EvidenceAt),
		Reason: healthReasonBody(instance.Health.Reason),
	}
	if instance.Health.SourceObservedAt != nil {
		value := formatTime(*instance.Health.SourceObservedAt)
		health.SourceObservedAt = &value
	}
	if instance.Health.Runtime != nil {
		runtime := instance.Health.Runtime
		health.Runtime = &AdapterRuntimeEvidenceBody{
			ID: string(runtime.ID), Status: runtime.Status, SoftwareName: runtime.SoftwareName,
			SoftwareVersion: runtime.SoftwareVersion, ClaimedAt: formatTime(runtime.ClaimedAt),
			LeaseExpiresAt: formatTime(runtime.LeaseExpiresAt),
		}
		if runtime.LastHeartbeatAt != nil {
			value := formatTime(*runtime.LastHeartbeatAt)
			health.Runtime.LastHeartbeatAt = &value
		}
	}
	return AdapterBody{ID: instance.ID, Health: health}
}

func healthReasonBody(reason *adapters.HealthReason) *AdapterHealthReasonBody {
	if reason == nil {
		return nil
	}
	return &AdapterHealthReasonBody{Code: reason.Code}
}
