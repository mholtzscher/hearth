// Package numstats accumulates and merges numeric samples without rounding sums.
// Persistence and the callers' range and null policies stay outside this package.
package numstats

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Number preserves integer extrema without passing them through float64.
type Number struct {
	Int    int64
	Float  float64
	IsReal bool
}

func Integer(value int64) Number { return Number{Int: value} }
func Real(value float64) Number  { return Number{Float: value, IsReal: true} }

// Value returns the numeric storage form without converting integers to floats.
func (number Number) Value() any {
	if number.IsReal {
		return number.Float
	}
	return number.Int
}

func (number Number) rational() *big.Rat {
	if number.IsReal {
		return new(big.Rat).SetFloat64(number.Float)
	}
	return new(big.Rat).SetInt64(number.Int)
}

func (number Number) Encode() (json.RawMessage, error) {
	if number.IsReal {
		return json.Marshal(number.Float)
	}
	return json.Marshal(number.Int)
}

type Kind int

const (
	Numeric Kind = iota + 1
	Null
	Invalid
)

type Sample struct {
	Kind   Kind
	Number Number
}

// Select extracts a root or JSON-pointer field from normalized State JSON.
// Absence and known null count as null; present nonnumbers count as invalid.
func Select(value json.RawMessage, field string) (Sample, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var selected any
	if err := decoder.Decode(&selected); err != nil {
		return Sample{}, fmt.Errorf("decode numeric sample: %w", err)
	}
	if field != "" {
		for token := range strings.SplitSeq(strings.TrimPrefix(field, "/"), "/") {
			object, ok := selected.(map[string]any)
			if !ok {
				return Sample{Kind: Null}, nil
			}
			if selected, ok = object[token]; !ok {
				return Sample{Kind: Null}, nil
			}
		}
	}
	if selected == nil {
		return Sample{Kind: Null}, nil
	}
	token, ok := selected.(json.Number)
	if !ok {
		return Sample{Kind: Invalid}, nil
	}
	if !strings.ContainsAny(string(token), ".eE") {
		if integer, err := token.Int64(); err == nil {
			return Sample{Kind: Numeric, Number: Integer(integer)}, nil
		}
	}
	valueNumber, err := token.Float64()
	if err != nil || math.IsInf(valueNumber, 0) || math.IsNaN(valueNumber) {
		return Sample{Kind: Invalid}, nil
	}
	return Sample{Kind: Numeric, Number: Real(valueNumber)}, nil
}

// Stats is a mergeable set of samples. Its zero value is an empty set.
// Sum is exact for the decoded integer and binary floating-point samples.
// Copy with Merge, not a struct assignment: Sum owns mutable [big.Int] storage.
type Stats struct {
	Count   int64
	Nulls   int64
	Invalid int64
	Sum     big.Rat
	Min     Number
	Max     Number
}

func (stats *Stats) Add(sample Sample) {
	switch sample.Kind {
	case Null:
		stats.Nulls++
	case Invalid:
		stats.Invalid++
	case Numeric:
		stats.addExtrema(sample.Number, sample.Number)
		stats.Sum.Add(&stats.Sum, sample.Number.rational())
		stats.Count++
	}
}

func (stats *Stats) Merge(other *Stats) {
	if other.Count > 0 {
		stats.addExtrema(other.Min, other.Max)
		stats.Sum.Add(&stats.Sum, &other.Sum)
		stats.Count += other.Count
	}
	stats.Nulls += other.Nulls
	stats.Invalid += other.Invalid
}

func (stats *Stats) addExtrema(minimum, maximum Number) {
	if stats.Count == 0 || minimum.rational().Cmp(stats.Min.rational()) < 0 {
		stats.Min = minimum
	}
	if stats.Count == 0 || maximum.rational().Cmp(stats.Max.rational()) > 0 {
		stats.Max = maximum
	}
}

// Mean rounds once, after dividing the exact sum by the sample count.
func (stats *Stats) Mean() (json.RawMessage, error) {
	if stats.Count == 0 {
		return json.RawMessage("null"), nil
	}
	mean := new(big.Rat).Quo(&stats.Sum, new(big.Rat).SetInt64(stats.Count))
	value, _ := mean.Float64()
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, errors.New("numeric mean is not finite")
	}
	return json.RawMessage(strconv.FormatFloat(value, 'g', -1, 64)), nil
}
