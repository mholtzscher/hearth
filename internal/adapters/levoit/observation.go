package levoit

import (
	"context"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func (a *Adapter) publish(ctx context.Context, runtime *deviceRuntime, report status,
	linkedKey string, evidence adapter.CommandEvidence) error {
	var reports []adapter.EntityAvailabilityReport
	// A linked report is a partial optimistic control update. Omitted fields must
	// not change sibling availability; full scheduled reports still withdraw it.
	if linkedKey != "" {
		for _, plan := range runtime.plans {
			if plan.descriptor.Key != linkedKey {
				continue
			}
			entry, err := a.publishEntity(ctx, runtime, plan, report, evidence)
			if err != nil {
				return err
			}
			reports = append(reports, entry)
		}
	}
	for _, plan := range runtime.plans {
		if plan.descriptor.Key == linkedKey {
			continue
		}
		entry, err := a.publishEntity(ctx, runtime, plan, report, nil)
		if err != nil {
			return err
		}
		if linkedKey != "" && entry.Status == adapter.AvailabilityUnavailable {
			continue
		}
		reports = append(reports, entry)
	}
	return a.session.ReportEntityAvailability(ctx, reports)
}

func (a *Adapter) publishEntity(ctx context.Context, runtime *deviceRuntime, plan entityPlan,
	report status, evidence adapter.CommandEvidence) (adapter.EntityAvailabilityReport, error) {
	id := runtime.ids[plan.descriptor.Key]
	entry := adapter.EntityAvailabilityReport{EntityID: id, SourceObservedAt: report.ReceivedAt,
		Status: adapter.AvailabilityUnavailable, ReasonCode: reasonPrefix + "measurement_unavailable"}
	observation, present, err := plan.observe(id, report)
	if err != nil {
		return entry, err
	}
	if !present {
		return entry, nil
	}
	if evidence == nil {
		_, err = a.session.PublishObservation(ctx, observation)
	} else {
		_, err = evidence.PublishObservation(ctx, observation)
	}
	if err != nil {
		return entry, err
	}
	entry.Status, entry.ReasonCode = adapter.AvailabilityAvailable, ""
	return entry, nil
}

func (a *Adapter) unavailable(ctx context.Context, runtime *deviceRuntime, reason string) error {
	reports := make([]adapter.EntityAvailabilityReport, 0, len(runtime.ids))
	now := time.Now().UTC()
	for _, id := range runtime.ids {
		reports = append(reports, adapter.EntityAvailabilityReport{EntityID: id,
			SourceObservedAt: now, Status: adapter.AvailabilityUnavailable, ReasonCode: reasonPrefix + reason})
	}
	if len(reports) == 0 {
		return nil
	}
	return a.session.ReportEntityAvailability(ctx, reports)
}
