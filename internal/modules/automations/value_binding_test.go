package automations //nolint:testpackage // Preparation is an internal definition boundary.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValueBindingPreparationOwnsLiteralAndRejectsInvalidExpression(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"expr":"not code","n":9007199254740993}`)
	prepared, err := normalizeValueBinding(ValueBinding{Body: LiteralValue{Value: raw}})
	require.NoError(t, err)
	raw[0] = '['
	owned := prepared.Body.(LiteralValue).Value
	require.JSONEq(t, `{"expr":"not code","n":9007199254740993}`, string(owned))
	require.Contains(t, string(owned), "9007199254740993")
	_, err = normalizeValueBinding(ValueBinding{Body: ExpressionValue{Source: `vars.`}})
	require.ErrorIs(t, err, ErrInvalidAutomation)
}
