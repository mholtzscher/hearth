package dbload_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeDoesNotDeclareImprovementBeforeApplyingFamilyAlpha(t *testing.T) {
	t.Parallel()
	ratios := constantRatios(1)
	for i := range 6 {
		ratios[i] = 0.3
	}
	comparison := analyzeSingle(t, testRegistration("alpha-boundary"), completePairs("alpha-boundary", ratios))
	require.Greater(
		t,
		comparison.AdjustedP,
		testAlpha,
		"six directional nonzero pairs do not clear the registered family alpha",
	)
	require.Equal(
		t,
		"inconclusive",
		comparison.Verdict,
		"a favorable interval and practical effect cannot bypass the significance test",
	)
}
