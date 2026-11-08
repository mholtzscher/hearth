package numstats_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/numstats"
)

func collect(t *testing.T, values ...string) *numstats.Stats {
	t.Helper()
	stats := new(numstats.Stats)
	for _, value := range values {
		sample, err := numstats.Select(json.RawMessage(value), "")
		if err != nil {
			t.Fatal(err)
		}
		stats.Add(sample)
	}
	return stats
}

func TestExactSumsSurviveOverflowAndCancellation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		values    []string
		sum, mean string
	}{
		{"beyond int64", []string{"9223372036854775807", "9223372036854775807"}, "18446744073709551614", "9.223372036854776e+18"},
		{"cancellation", []string{"10000000000000000", "0.1", "-10000000000000000"}, "3602879701896397/36028797018963968", "0.03333333333333333"},
		{"float overflow", []string{"1e308", "1e308", "-1e308"}, "", "3.333333333333333e+307"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			all := collect(t, test.values...)
			merged := new(numstats.Stats)
			for _, value := range test.values {
				merged.Merge(collect(t, value))
			}
			if all.Sum.Cmp(&merged.Sum) != 0 {
				t.Fatal("merge changed the sum")
			}
			if test.sum != "" && all.Sum.RatString() != test.sum {
				t.Fatalf("sum %s, want %s", &all.Sum, test.sum)
			}
			mean, err := all.Mean()
			if err != nil || string(mean) != test.mean {
				t.Fatalf("mean %s, %v, want %s", mean, err, test.mean)
			}
		})
	}
}

func TestMergeRetainsNullsInvalidAndExactExtrema(t *testing.T) {
	t.Parallel()
	stats := collect(t, "9007199254740993", "9007199254740992.0", "null", "true")
	if stats.Count != 2 || stats.Nulls != 1 || stats.Invalid != 1 {
		t.Fatalf("counts: %+v", stats)
	}
	minimum, err := stats.Min.Encode()
	if err != nil || string(minimum) != "9007199254740992" {
		t.Fatalf("minimum: %s, %v", minimum, err)
	}
	maximum, err := stats.Max.Encode()
	if err != nil || string(maximum) != "9007199254740993" {
		t.Fatalf("maximum: %s, %v", maximum, err)
	}
	merged := new(numstats.Stats)
	merged.Merge(stats)
	stats.Add(numstats.Sample{Kind: numstats.Numeric, Number: numstats.Integer(1)})
	if merged.Count != 2 || merged.Sum.Cmp(&stats.Sum) == 0 {
		t.Fatal("merge aliased its source")
	}
}
