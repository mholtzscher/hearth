package numstats_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/numstats"
)

func TestSelectClassifiesNumericFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, value, field string
		want               numstats.Sample
	}{
		{"root integer", `21300`, "", numstats.Sample{Kind: numstats.Numeric, Number: numstats.Integer(21300)}},
		{"root decimal", `40.5`, "", numstats.Sample{Kind: numstats.Numeric, Number: numstats.Real(40.5)}},
		{"integral decimal stays real", `45.0`, "", numstats.Sample{Kind: numstats.Numeric, Number: numstats.Real(45)}},
		{"exponent is real", `1e3`, "", numstats.Sample{Kind: numstats.Numeric, Number: numstats.Real(1000)}},
		{"integer beyond int64 is real", `9223372036854775808`, "", numstats.Sample{Kind: numstats.Numeric, Number: numstats.Real(9223372036854775808)}},
		{"nested field", `{"active":true,"value":250}`, "/value", numstats.Sample{Kind: numstats.Numeric, Number: numstats.Integer(250)}},
		{"known null", `{"mode":"choice","value":null}`, "/value", numstats.Sample{Kind: numstats.Null}},
		{"absent field", `{"mode":"choice","choice":"auto"}`, "/value", numstats.Sample{Kind: numstats.Null}},
		{"field beneath non-object", `7`, "/value", numstats.Sample{Kind: numstats.Null}},
		{"root null", `null`, "", numstats.Sample{Kind: numstats.Null}},
		{"boolean", `{"value":true}`, "/value", numstats.Sample{Kind: numstats.Invalid}},
		{"string", `"21"`, "", numstats.Sample{Kind: numstats.Invalid}},
		{"object", `{"value":{"x":1}}`, "/value", numstats.Sample{Kind: numstats.Invalid}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := numstats.Select(json.RawMessage(test.value), test.field)
			if err != nil || got != test.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, test.want)
			}
		})
	}
	if _, err := numstats.Select(json.RawMessage(`{`), ""); err == nil {
		t.Fatal("malformed JSON should fail")
	}
}
