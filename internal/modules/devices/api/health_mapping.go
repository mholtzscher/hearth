package api

import "github.com/mholtzscher/hearth/internal/modules/devices"

func adapterBody(instance devices.AdapterInstance) AdapterBody {
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

func healthTransitionBody(transition devices.HealthTransition) HealthTransitionBody {
	body := HealthTransitionBody{
		Status: transition.Status, Source: transition.Source,
		Reason: healthReasonBody(transition.Reason), ObservedAt: formatTime(transition.ObservedAt),
	}
	if transition.SourceObservedAt != nil {
		value := formatTime(*transition.SourceObservedAt)
		body.SourceObservedAt = &value
	}
	return body
}

func healthReasonBody(reason *devices.HealthReason) *HealthReasonBody {
	if reason == nil {
		return nil
	}
	return &HealthReasonBody{Code: reason.Code}
}
