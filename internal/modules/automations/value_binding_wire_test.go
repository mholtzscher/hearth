package automations //nolint:testpackage // Canonical definition ingress owns wire alternatives.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefinitionValueAlternatives(t *testing.T) {
	t.Parallel()
	const prefix = `{"name":"Values","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],"steps":[`
	const command = `"id":"c","kind":"command","entity_id":"ent_01980000-0000-7000-8000-000000000001","operation":"set"`
	const function = `"id":"f","kind":"function","adapter_id":"weather","function_key":"forecast","contract_fingerprint":"sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`
	const notification = `"id":"n","kind":"notification","destination_id":"ndst_01980000-0000-7000-8000-000000000001"`
	for _, test := range []struct {
		step  string
		valid bool
	}{
		{`{` + command + `,"parameters":{"expr":"ordinary data","n":9007199254740993}}`, true},
		{`{` + command + `,"parameters_expr":"let target = vars.target ?? 22; {value: target}"}`, true},
		{`{` + command + `}`, false},
		{`{` + command + `,"parameters":{},"parameters_expr":"{}"}`, false},
		{`{` + command + `,"parameters":null}`, false},
		{`{` + command + `,"parameters_expr":" "}`, false},
		{`{` + command + `,"parameters_expr":{}}`, false},
		{`{` + function + `,"arguments":{"expr":"vendor data"}}`, true},
		{`{` + function + `,"arguments_expr":"{hours: int('3')}"}`, true},
		{`{` + function + `,"arguments":{},"arguments_expr":"{}"}`, false},
		{`{` + function + `}`, false},
		{`{` + notification + `,"title":"Heating update","message_expr":"'Target: ' + string(vars.target)"}`, true},
		{`{` + notification + `,"title_expr":"'Heating update'","message":"Target"}`, true},
		{`{` + notification + `,"title":"a","title_expr":"'b'","message":"c"}`, false},
		{`{` + notification + `,"title":"a","message":"b","message_expr":"'c'"}`, false},
		{`{` + notification + `,"title":"a"}`, false},
		{`{` + notification + `,"title":{"literal":"a"},"message":"b"}`, false},
		{`{"id":"wait","kind":"delay","duration_ms":2000}`, true},
		{`{"id":"wait","kind":"delay","duration_ms":{"literal":2000}}`, false},
		{`{"id":"wait","kind":"delay","duration_ms_expr":"2000"}`, false},
		{`{"id":"set","kind":"set_variables","assignments":{"reason":"forecast"},"assignments_expr":{"target":"steps.forecast?.output ?? 20"}}`, true},
		{`{"id":"set","kind":"set_variables","assignments":{"target":22},"assignments_expr":{"target":"23"}}`, false},
		{`{"id":"set","kind":"set_variables","assignments":{},"assignments_expr":{}}`, false},
		{`{"id":"set","kind":"set_variables","assignments_expr":{"target":{"expr":"22"}}}`, false},
		{`{"id":"if","kind":"if","conditions":{"id":"cold","kind":"expression","expr":"steps.current?.output < vars.target"},"then":[{"id":"d","kind":"delay","duration_ms":1}]}`, true},
		{`{"id":"if","kind":"if","conditions":{"id":"cold","kind":"expression","value":{"expr":"true"}},"then":[{"id":"d","kind":"delay","duration_ms":1}]}`, false},
	} {
		t.Run(test.step, func(t *testing.T) {
			t.Parallel()
			definition, err := DecodeDefinition(json.RawMessage(prefix + test.step + `]}`))
			if !test.valid {
				require.ErrorIs(t, err, ErrInvalidAutomation)
				return
			}
			require.NoError(t, err)
			raw, err := EncodeDefinition(definition)
			require.NoError(t, err)
			var encoded struct {
				Steps []json.RawMessage `json:"steps"`
			}
			require.NoError(t, json.Unmarshal(raw, &encoded))
			actual, err := decodeJSONValue(encoded.Steps[0])
			require.NoError(t, err)
			expected, err := decodeJSONValue(json.RawMessage(test.step))
			require.NoError(t, err)
			require.Equal(t, expected, actual)
			_, err = DecodeDefinition(raw)
			require.NoError(t, err)
		})
	}
}

func TestInitialVariableWireAlternatives(t *testing.T) {
	t.Parallel()
	const prefix = `{"name":"Values","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"* * * * *"}],`
	const suffix = `,"steps":[{"id":"d","kind":"delay","duration_ms":1}]}`
	for _, test := range []struct {
		fields string
		valid  bool
	}{
		{`"variables":{"target":9007199254740993,"vendor":{"expr":"not code"}},"variables_expr":{"origin":"trigger.kind"}`, true},
		{`"variables":{"target":22},"variables_expr":{"target":"23"}`, false},
		{`"variables_expr":{"x":"vars.target"}`, false},
		{`"variables_expr":{"x":"steps.read?.output"}`, false},
		{`"variables_expr":{"x":"$env.vars.target"}`, false},
		{`"variables_expr":{"x":null}`, false},
		{`"variables_expr":{"x":" "}`, false},
	} {
		definition, err := DecodeDefinition(json.RawMessage(prefix + test.fields + suffix))
		if !test.valid {
			require.ErrorIs(t, err, ErrInvalidAutomation)
			continue
		}
		require.NoError(t, err)
		raw, err := EncodeDefinition(definition)
		require.NoError(t, err)
		require.Contains(t, string(raw), "9007199254740993")
		require.Contains(t, string(raw), `"variables_expr":{"origin":"trigger.kind"}`)
	}
	literals := make([]string, 64)
	for index := range literals {
		literals[index] = `"` + strings.Repeat("x", index/2+1) + string(rune('a'+index%2)) + `":0`
	}
	_, err := DecodeDefinition(json.RawMessage(prefix + `"variables":{` + strings.Join(literals, ",") + `}` + suffix))
	require.NoError(t, err)
	_, err = DecodeDefinition(
		json.RawMessage(
			prefix + `"variables":{` + strings.Join(literals, ",") + `},"variables_expr":{"overflow":"1"}` + suffix,
		),
	)
	require.ErrorIs(t, err, ErrInvalidAutomation)
}
