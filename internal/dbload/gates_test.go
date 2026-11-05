package dbload_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/dbload"
)

func TestAbsoluteGatesDoNotConfuseImprovementWithThresholdEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		class   string
		latency float64
		want    string
	}{
		{"bounded below", "bounded-read", 100, "pass"},
		{"bounded above", "bounded-read", 300, "fail"},
		{"exact threshold", "bounded-read", 250, "at threshold / inconclusive"},
		{"write", "write", 500, "pass"},
		{"unbounded", "unbounded-read", 500, "not applicable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			blocks := make([]dbload.Block, 20)
			for i := range blocks {
				blocks[i] = dbload.Block{
					Case:        "case",
					Class:       test.class,
					Scenario:    "population-1",
					Variant:     "baseline",
					Pair:        i + 1,
					Concurrency: 1,
					Attempted:   200,
					Completed:   200,
					P95MS:       test.latency,
					Status:      "complete",
				}
			}
			gate := dbload.EvaluateGate(blocks, 1)
			require.Equal(t, test.want, gate.Status)
			blocks[0].Timeouts = 1
			require.NotEqual(t, "pass", dbload.EvaluateGate(blocks, 1).Status)
			blocks[0].Timeouts = 0
			blocks[0].Concurrency = 16
			require.NotEqual(t, "pass", dbload.EvaluateGate(blocks, 1).Status)
		})
	}
}
