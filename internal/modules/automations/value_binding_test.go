package automations //nolint:testpackage // Preparation is an internal definition boundary.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValueBindingCodec(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"literal":null}`,
		`{"literal":{"expr":"not code","n":9007199254740993}}`,
		`{"expr":"vars.target + 1"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			var binding ValueBinding
			require.NoError(t, json.Unmarshal([]byte(raw), &binding))
			prepared, err := normalizeValueBinding(binding)
			require.NoError(t, err)
			encoded, err := json.Marshal(prepared)
			require.NoError(t, err)
			require.Equal(t, raw, string(encoded))
			require.JSONEq(t, raw, string(encoded))
		})
	}
	for _, raw := range []string{
		`null`, `{}`, `{"expr":""}`, `{"expr":null}`,
		`{"literal":1,"expr":"1"}`, `{"literal":1,"literal":2}`, `{"other":1}`,
	} {
		t.Run("reject "+raw, func(t *testing.T) {
			t.Parallel()
			var binding ValueBinding
			require.Error(t, json.Unmarshal([]byte(raw), &binding))
		})
	}
}

func TestValueBindingPreparationOwnsLiteralAndRejectsInvalidExpression(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"n":9007199254740993}`)
	prepared, err := normalizeValueBinding(ValueBinding{Body: LiteralValue{Value: raw}})
	require.NoError(t, err)
	raw[0] = '['
	require.Equal(t, `{"n":9007199254740993}`, string(prepared.Body.(LiteralValue).Value))
	_, err = normalizeValueBinding(ValueBinding{Body: ExpressionValue{Source: `vars.foo?.bar`}})
	require.ErrorIs(t, err, ErrInvalidAutomation)
	var binding ValueBinding
	require.Error(t, binding.UnmarshalJSON([]byte(`{"literal":1} {}`)))
}
