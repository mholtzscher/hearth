package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
