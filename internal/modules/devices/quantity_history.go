package devices

import (
	"encoding/json"
	"errors"
)

// History projects after canonical aggregation. Each metric remains at its
// existing location and nulls retain the source's gap semantics.
func (presenter *Presenter) History(
	view EntityWithState,
	field string,
	result HistoryQueryResult,
	units ReadUnits,
) (HistoryQueryResult, QuantityMetadata, error) {
	definition, exists := BuiltinStateField(view.Entity.TypeID, field)
	if !exists {
		return HistoryQueryResult{}, QuantityMetadata{}, errors.New("history quantity field is not catalogued")
	}
	numeric, ok := definition.Value.(NumericField)
	if !ok {
		return HistoryQueryResult{}, QuantityMetadata{}, errors.New("history quantity field is not numeric")
	}
	presentation, err := presenter.field(numeric, json.RawMessage(view.Entity.Support), units)
	if err != nil {
		return HistoryQueryResult{}, QuantityMetadata{}, err
	}
	project := func(values HistoryMetricValues) (HistoryMetricValues, error) {
		converted := make(HistoryMetricValues, len(values))
		for name, value := range values {
			number, numberErr := presentation.number(value)
			if numberErr != nil {
				return nil, numberErr
			}
			converted[name] = number
		}
		return converted, nil
	}
	switch data := result.Data.(type) {
	case HistorySummaryResult:
		data.Metrics, err = project(data.Metrics)
		if err != nil {
			return HistoryQueryResult{}, QuantityMetadata{}, err
		}
		result.Data = data
	case HistoryBucketedSummaryResult:
		buckets := make([]HistorySummaryBucket, len(data.Buckets))
		for index, bucket := range data.Buckets {
			buckets[index] = bucket
			buckets[index].Metrics, err = project(bucket.Metrics)
			if err != nil {
				return HistoryQueryResult{}, QuantityMetadata{}, err
			}
		}
		data.Buckets = buckets
		result.Data = data
	default:
		return HistoryQueryResult{}, QuantityMetadata{}, errors.New("unsupported history result")
	}
	return result, presentation.metadata, err
}
