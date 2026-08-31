package api

import "github.com/mholtzscher/hearth/internal/modules/devices"

func adapterBody(instance devices.AdapterInstance) AdapterBody {
	body := AdapterBody{ID: instance.ID}
	if instance.ArchivedAt != nil {
		value := formatTime(*instance.ArchivedAt)
		body.ArchivedAt = &value
	}
	if instance.Health == nil {
		return body
	}
	health := &AdapterHealthBody{
		Status: string(instance.Health.Status), Since: formatTime(instance.Health.Since),
		EvidenceAt: formatTime(instance.Health.EvidenceAt), Reason: healthReasonBody(instance.Health.Reason),
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
	if instance.Health.ExternalSystem != nil {
		external := instance.Health.ExternalSystem
		health.ExternalSystem = &ExternalSystemEvidenceBody{
			Status: string(external.Status), SourceObservedAt: formatTime(external.SourceObservedAt),
			EvidenceAt: formatTime(external.EvidenceAt), Reason: healthReasonBody(external.Reason),
		}
	}
	body.Health = health
	return body
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
	body := &HealthReasonBody{Code: reason.Code}
	if reason.Detail != nil {
		detail := *reason.Detail
		body.Detail = &detail
	}
	return body
}
