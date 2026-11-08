package devices //nolint:testpackage // Exercises unexported rollup rendering helpers.

import (
	"slices"
	"testing"
)

func TestClassifyHistoryRollupSample(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		value string
		field string
		want  HistoryRollupSample
	}{
		{"root integer", `21300`, "", HistoryRollupSample{Kind: HistoryRollupNumeric, Number: HistoryIntNumber(21300)}},
		{"root decimal", `40.5`, "", HistoryRollupSample{Kind: HistoryRollupNumeric, Number: HistoryRealNumber(40.5)}},
		{"integral decimal stays real", `45.0`, "", HistoryRollupSample{Kind: HistoryRollupNumeric, Number: HistoryRealNumber(45)}},
		{"exponent is real", `1e3`, "", HistoryRollupSample{Kind: HistoryRollupNumeric, Number: HistoryRealNumber(1000)}},
		{"integer beyond int64 is real", `9223372036854775808`, "",
			HistoryRollupSample{Kind: HistoryRollupNumeric, Number: HistoryRealNumber(9223372036854775808)}},
		{"nested field", `{"active":true,"value":250}`, "/value",
			HistoryRollupSample{Kind: HistoryRollupNumeric, Number: HistoryIntNumber(250)}},
		{"known null", `{"mode":"choice","value":null}`, "/value", HistoryRollupSample{Kind: HistoryRollupNull}},
		{"absent field", `{"mode":"choice","choice":"auto"}`, "/value", HistoryRollupSample{Kind: HistoryRollupNull}},
		{"field beneath non-object", `7`, "/value", HistoryRollupSample{Kind: HistoryRollupNull}},
		{"root null", `null`, "", HistoryRollupSample{Kind: HistoryRollupNull}},
		{"boolean", `{"value":true}`, "/value", HistoryRollupSample{Kind: HistoryRollupInvalid}},
		{"string", `"21"`, "", HistoryRollupSample{Kind: HistoryRollupInvalid}},
		{"object", `{"value":{"x":1}}`, "/value", HistoryRollupSample{Kind: HistoryRollupInvalid}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ClassifyHistoryRollupSample(Value(test.value), test.field)
			if err != nil || got != test.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, test.want)
			}
		})
	}
	if _, err := ClassifyHistoryRollupSample(Value(`{`), ""); err == nil {
		t.Fatal("malformed JSON should fail")
	}
}

func TestHistoryRollupFieldsListsOnlyNumericFields(t *testing.T) {
	t.Parallel()
	if got := HistoryRollupFields(EntityTypeColortempV1); !slices.Equal(got, []string{"/value"}) {
		t.Fatalf("colortemp fields = %v, want [/value]", got)
	}
	if got := HistoryRollupFields(EntityTypePowerV1); len(got) != 0 {
		t.Fatalf("power fields = %v, want none", got)
	}
	if got := HistoryRollupFields(EntityTypeTemperatureV1); !slices.Equal(got, []string{""}) {
		t.Fatalf("temperature fields = %v, want root", got)
	}
}
