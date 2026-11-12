package august

import (
	"context"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/binarysensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter/lockv1"
	"github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
)

// publish is called under the physical lock's gate. Ordering advances only after publication succeeds.
func (a *Adapter) publish(ctx context.Context, runtime *deviceRuntime, report report, keys []string) error {
	availability := make([]adapter.EntityAvailabilityReport, 0, len(keys))
	for _, key := range keys {
		id := runtime.ids[key]
		field := runtime.fields[key]
		if field == nil {
			field = &fieldRuntime{}
			runtime.fields[key] = field
		}
		observation, source, present, err := fieldObservation(id, key, report)
		if err != nil {
			return err
		}
		if !present {
			field.available = false
		} else if field.accepts(source, string(observation.Value)) {
			if _, err = a.session.PublishObservation(ctx, observation); err != nil {
				return err
			}
			field.available = true
			field.value = string(observation.Value)
			if source != nil {
				field.source = source
			}
		}
		entry := adapter.EntityAvailabilityReport{EntityID: id, Status: adapter.AvailabilityUnavailable,
			SourceObservedAt: report.ReceivedAt, ReasonCode: reasonPrefix + "field_invalid"}
		if field.available {
			entry.Status, entry.ReasonCode = adapter.AvailabilityAvailable, ""
		}
		availability = append(availability, entry)
	}
	return a.session.ReportEntityAvailability(ctx, availability)
}

func (field *fieldRuntime) accepts(source *time.Time, value string) bool {
	if field.source == nil {
		return true
	}
	if source == nil || source.Before(*field.source) {
		return false
	}
	return !source.Equal(*field.source) || value == field.value
}

func fieldObservation(id, key string, r report) (adapter.Observation, *time.Time, bool, error) {
	switch key {
	case keyLock:
		if r.Lock != nil {
			observation, err := lockv1.NewObservation(lockv1.ObservationInput{EntityID: id, Support: lockv1.Support{},
				State: r.Lock.Value, AdapterReceivedAt: r.ReceivedAt, SourceUpdatedAt: r.Lock.SourceUpdatedAt})
			return observation, r.Lock.SourceUpdatedAt, true, err
		}
	case keyDoorOpen:
		if r.DoorOpen != nil {
			observation, err := binarysensorv1.NewObservation(
				binarysensorv1.ObservationInput{
					EntityID: id,
					Support:  binarysensorv1.Support{},
					State: binarysensorv1.State(
						r.DoorOpen.Value,
					),
					AdapterReceivedAt: r.ReceivedAt,
					SourceUpdatedAt:   r.DoorOpen.SourceUpdatedAt,
				},
			)
			return observation, r.DoorOpen.SourceUpdatedAt, true, err
		}
	case keyBattery:
		if r.Battery != nil {
			observation, err := numericsensorv1.NewObservation(
				numericsensorv1.ObservationInput{
					EntityID: id,
					Support:  batterySupport(),
					State: numericsensorv1.State(
						r.Battery.Value,
					),
					AdapterReceivedAt: r.ReceivedAt,
					SourceUpdatedAt:   r.Battery.SourceUpdatedAt,
				},
			)
			return observation, r.Battery.SourceUpdatedAt, true, err
		}
	}
	return adapter.Observation{}, nil, false, nil
}

func invalidateRuntime(runtime *deviceRuntime) {
	runtime.online = false
	for _, field := range runtime.fields {
		field.available = false
	}
}

func (a *Adapter) unavailable(ctx context.Context, runtime *deviceRuntime, reason string) error {
	invalidateRuntime(runtime)
	reports := make([]adapter.EntityAvailabilityReport, 0, len(runtime.ids))
	for _, id := range runtime.ids {
		reports = append(
			reports,
			adapter.EntityAvailabilityReport{EntityID: id, Status: adapter.AvailabilityUnavailable,
				SourceObservedAt: time.Now().UTC(), ReasonCode: reasonPrefix + reason},
		)
	}
	if len(reports) == 0 {
		return nil
	}
	return a.session.ReportEntityAvailability(ctx, reports)
}
