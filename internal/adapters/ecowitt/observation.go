package ecowitt

import (
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// projectedMeasurement is one Entity's typed Observation together with the
// catalog route and normalized value that produced it. Tests use the
// normalized value as an independent conversion oracle.
type projectedMeasurement struct {
	Route       entityRoute
	Observation adapter.Observation
	Value       normalizedState
}

// projectReport walks the catalog in registration order and builds exactly one
// typed Observation for every present, decodable measurement of one accepted
// report. It returns the invalid measurement count so the caller can classify
// isolated field failures without logging vendor values.
//
// Each Entity is independent: an absent, malformed, non-finite, or
// out-of-envelope field produces no Observation and never suppresses a valid
// sibling. One MQTT receipt time becomes adapter_received_at for every
// Observation of the report, and a sane parsed dateutc becomes the shared
// optional source_updated_at.
func projectReport(
	routes routeSnapshot,
	report stationReport,
	receivedAt time.Time,
) ([]projectedMeasurement, int) {
	projected := make([]projectedMeasurement, 0, len(routes.entities))
	invalid := 0
	for _, route := range routes.entities {
		raw, present := report.Fields[route.Plan.Field]
		if !present {
			continue
		}
		value, err := route.Plan.Decode(raw)
		if err != nil {
			invalid++
			continue
		}
		observation, err := newQuantityObservation(
			route.Plan.Kind,
			route.EntityID,
			value,
			route.Plan.Unit,
			route.Plan.Minimum,
			route.Plan.Maximum,
			receivedAt,
			report.SourceTime,
		)
		if err != nil {
			invalid++
			continue
		}
		projected = append(projected, projectedMeasurement{
			Route:       route,
			Observation: observation,
			Value:       value,
		})
	}
	return projected, invalid
}
