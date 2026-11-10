package pirateweather

import (
	"errors"

	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter/pressurev1"
	"github.com/mholtzscher/hearth/sdk/adapter/relativehumidityv1"
	"github.com/mholtzscher/hearth/sdk/adapter/speedv1"
	"github.com/mholtzscher/hearth/sdk/adapter/temperaturev1"
)

func (m measurement) observation(id string, value float64, report currentResult) (adapter.Observation, error) {
	switch m.kind {
	case temperature:
		return temperaturev1.NewObservation(temperaturev1.ObservationInput{
			EntityID: id, Support: temperaturev1.Support{}, State: temperaturev1.State(value),
			AdapterReceivedAt: report.ReceivedAt, SourceUpdatedAt: &report.SourceTime,
		})
	case humidity:
		return relativehumidityv1.NewObservation(relativehumidityv1.ObservationInput{
			EntityID: id, Support: relativehumidityv1.Support{}, State: relativehumidityv1.State(value),
			AdapterReceivedAt: report.ReceivedAt, SourceUpdatedAt: &report.SourceTime,
		})
	case pressure:
		return pressurev1.NewObservation(pressurev1.ObservationInput{
			EntityID: id, Support: pressurev1.Support{}, State: pressurev1.State(value),
			AdapterReceivedAt: report.ReceivedAt, SourceUpdatedAt: &report.SourceTime,
		})
	case speed:
		return speedv1.NewObservation(speedv1.ObservationInput{
			EntityID: id, Support: speedv1.Support{}, State: speedv1.State(value),
			AdapterReceivedAt: report.ReceivedAt, SourceUpdatedAt: &report.SourceTime,
		})
	case numeric:
		return numericsensorv1.NewObservation(numericsensorv1.ObservationInput{
			EntityID: id, Support: m.numericSupport(), State: numericsensorv1.State(value),
			AdapterReceivedAt: report.ReceivedAt, SourceUpdatedAt: &report.SourceTime,
		})
	}
	return adapter.Observation{}, errors.New("invalid measurement kind")
}
