package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// HistoryRollupCellDuration is the fixed rollup resolution. Cells start at
// Unix-time multiples of it, in UTC, and are half-open.
const HistoryRollupCellDuration = time.Minute

// HistoryNumber keeps the int64/float64 distinction between JSON integer and
// decimal tokens, so encoded extrema keep their original numeric form.
type HistoryNumber struct {
	Int    int64
	Float  float64
	IsReal bool // false: Int holds the value; true: Float holds the value
}

// HistoryIntNumber and HistoryRealNumber construct the two numeric forms.
func HistoryIntNumber(value int64) HistoryNumber { return HistoryNumber{Int: value} }

func HistoryRealNumber(value float64) HistoryNumber { return HistoryNumber{Float: value, IsReal: true} }

// HistoryRollupSampleKind classifies one accepted report's field value.
type HistoryRollupSampleKind int

const (
	HistoryRollupNumeric HistoryRollupSampleKind = iota + 1
	HistoryRollupNull                            // known JSON null, or the field is absent
	HistoryRollupInvalid                         // present but not a number
)

type HistoryRollupSample struct {
	Kind   HistoryRollupSampleKind
	Number HistoryNumber // set only for HistoryRollupNumeric
}

// HistoryRollupBucket aggregates the cells whose start falls in one requested
// bucket. Sum, Min, and Max are meaningful only when Count > 0.
type HistoryRollupBucket struct {
	Index   int   // zero-based bucket position from the request's From
	Count   int64 // numeric samples
	Nulls   int64
	Invalid int64
	Sum     HistoryNumber // integer while every sample is an integer
	Min     HistoryNumber
	Max     HistoryNumber
}

type HistoryRollupRequest struct {
	EntityID EntityID
	Field    string        // validated against the Entity's type by the reader
	From     time.Time     // on the cell grid
	End      time.Time     // the cell boundary at or after the requested To
	Bucket   time.Duration // a positive multiple of HistoryRollupCellDuration
}

// HistoryRollupReader returns the non-empty buckets in ascending Index order,
// from one read snapshot. It returns ErrEntityNotFound for an unknown Entity,
// unsupported_source for a stateless type, and invalid_query on select_field
// when Field is not one of HistoryRollupFields for the Entity's type.
type HistoryRollupReader interface {
	ReadHistoryRollupBuckets(context.Context, HistoryRollupRequest) ([]HistoryRollupBucket, error)
}

// HistoryRollupFields returns the catalogued numeric field paths for typeID.
func HistoryRollupFields(typeID EntityTypeID) []string {
	var paths []string
	for _, field := range builtinStateFields[typeID] {
		if _, numeric := field.Value.(NumericField); numeric {
			paths = append(paths, field.Path)
		}
	}
	return paths
}

// ClassifyHistoryRollupSample extracts field ("" or a JSON pointer) from one
// accepted report's normalized value. An absent field, including one beneath a
// non-object, is a null sample; a present non-number is an invalid sample.
func ClassifyHistoryRollupSample(value Value, field string) (HistoryRollupSample, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var selected any
	if err := decoder.Decode(&selected); err != nil {
		return HistoryRollupSample{}, fmt.Errorf("decode history rollup value: %w", err)
	}
	if field != "" {
		for token := range strings.SplitSeq(strings.TrimPrefix(field, "/"), "/") {
			object, ok := selected.(map[string]any)
			if !ok {
				return HistoryRollupSample{Kind: HistoryRollupNull}, nil
			}
			if selected, ok = object[token]; !ok {
				return HistoryRollupSample{Kind: HistoryRollupNull}, nil
			}
		}
	}
	if selected == nil {
		return HistoryRollupSample{Kind: HistoryRollupNull}, nil
	}
	token, ok := selected.(json.Number)
	if !ok {
		return HistoryRollupSample{Kind: HistoryRollupInvalid}, nil
	}
	number, ok := parseHistoryNumber(string(token))
	if !ok {
		return HistoryRollupSample{Kind: HistoryRollupInvalid}, nil
	}
	return HistoryRollupSample{Kind: HistoryRollupNumeric, Number: number}, nil
}

// parseHistoryNumber mirrors SQLite json_extract: integer tokens within int64
// are integers; decimal, exponent, and out-of-range integer tokens are reals.
func parseHistoryNumber(token string) (HistoryNumber, bool) {
	if !strings.ContainsAny(token, ".eE") {
		if value, err := strconv.ParseInt(token, 10, 64); err == nil {
			return HistoryIntNumber(value), true
		}
	}
	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return HistoryNumber{}, false
	}
	return HistoryRealNumber(value), true
}

func (number HistoryNumber) encode() (json.RawMessage, error) {
	if number.IsReal {
		return json.Marshal(number.Float)
	}
	return json.Marshal(number.Int)
}

// historyMean divides an integer sum exactly before rounding once, so integer
// fields keep exact means; real sums are already rounded float totals.
func historyMean(sum HistoryNumber, count int64) (json.RawMessage, error) {
	mean := sum.Float / float64(count)
	if !sum.IsReal {
		mean, _ = new(big.Rat).SetFrac(big.NewInt(sum.Int), big.NewInt(count)).Float64()
	}
	if math.IsInf(mean, 0) || math.IsNaN(mean) {
		return nil, errors.New("history mean is not finite")
	}
	return json.RawMessage(strconv.FormatFloat(mean, 'g', -1, 64)), nil
}

// renderHistoryRollups applies the null policy to the read buckets and encodes
// each requested metric. A bucket without numeric samples renders null.
//
//nolint:gocognit // The policy checks, metric variants, and result budget share one pass.
func renderHistoryRollups(plan historyPlan, buckets []HistoryRollupBucket) (HistoryResult, error) {
	read := make([]*HistoryRollupBucket, len(plan.ranges))
	for i := range buckets {
		bucket := &buckets[i]
		if bucket.Index < 0 || bucket.Index >= len(read) || read[bucket.Index] != nil {
			return nil, fmt.Errorf("history rollup bucket index %d is outside the query", bucket.Index)
		}
		if bucket.Invalid > 0 || bucket.Nulls > 0 && plan.query.Nulls == HistoryNullReject {
			// Name the first metric, as every metric consumes the same values.
			return nil, historyError("value_type", "result.metrics."+plan.names[0])
		}
		read[bucket.Index] = bucket
	}
	rendered := make([]HistorySummaryBucket, len(plan.ranges))
	resultBytes := 0
	for i, bucketRange := range plan.ranges {
		values := make(HistoryMetricValues, len(plan.names))
		bucket := read[i]
		for _, name := range plan.names {
			value := json.RawMessage("null")
			if bucket != nil && bucket.Count > 0 && bucket.Nulls == 0 {
				var err error
				switch plan.query.Metrics[name].(type) {
				case HistoryMinimum:
					value, err = bucket.Min.encode()
				case HistoryMaximum:
					value, err = bucket.Max.encode()
				case HistorySampleMean:
					value, err = historyMean(bucket.Sum, bucket.Count)
				}
				if err != nil {
					return nil, err
				}
			}
			resultBytes += len(name) + len(value)
			if resultBytes > HistoryMaxResultBytes {
				return nil, historyError("query_limit", "result")
			}
			values[name] = value
		}
		rendered[i] = HistorySummaryBucket{Range: bucketRange, Metrics: values}
	}
	if plan.query.Bucket == nil {
		return HistorySummaryResult{Metrics: rendered[0].Metrics}, nil
	}
	return HistoryBucketedSummaryResult{Bucket: *plan.query.Bucket, Buckets: rendered}, nil
}
