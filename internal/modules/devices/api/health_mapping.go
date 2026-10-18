package api

import "github.com/mholtzscher/hearth/internal/modules/devices"

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
