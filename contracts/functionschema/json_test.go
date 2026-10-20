package functionschema_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/contracts/functionschema"
)

func TestNormalizeObjectRejectsLossyOrUnboundedValues(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]string{
		"null": "null", "array": "[]", "primitive": "1", "empty": "",
		"duplicate": `{"x":1,"x":2}`, "nested duplicate": `{"x":[{"y":1,"y":2}]}`,
		"escaped duplicate": `{"x":1,"\u0078":2}`, "trailing": `{} {}`, "nan": `{"x":NaN}`,
		"invalid utf8": "{\"x\":\"\xff\"}", "overflow items": `{"x":[` + strings.Repeat(`0,`, 1024) + `0]}`,
		"overflow bytes": `{"x":"` + strings.Repeat("x", 65536) + `"}`,
		"overflow depth": strings.Repeat(`{"x":`, 16) + `{}` + strings.Repeat(`}`, 16),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := functionschema.NormalizeObject(json.RawMessage(value))
			if !errors.Is(err, functionschema.ErrInvalidValue) {
				t.Fatalf("NormalizeObject error = %v", err)
			}
		})
	}
	for _, count := range []int{1024, 1025} {
		members := make([]string, count)
		for index := range count {
			members[index] = fmt.Sprintf(`"p%d":0`, index)
		}
		_, err := functionschema.NormalizeObject(json.RawMessage(`{` + strings.Join(members, ",") + `}`))
		if (count == 1024) != (err == nil) {
			t.Fatalf("member count %d: %v", count, err)
		}
	}
	for _, value := range []string{
		strings.Repeat(`{"x":`, 15) + `{}` + strings.Repeat(`}`, 15),
		`{"x":[` + strings.Repeat(`0,`, 1023) + `0]}`,
		`{"x":"` + strings.Repeat("x", 65528) + `"}`,
	} {
		if _, err := functionschema.NormalizeObject(json.RawMessage(value)); err != nil {
			t.Fatalf("boundary rejected: %v", err)
		}
	}
}

func TestNormalizeObjectPreservesNumberLiteralsAndIsStable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{"empty object", ` { } `, `{}`},
		{
			"sorted keys and exact numbers",
			` { "x": 1.0, "n": 9007199254740993, "nested": [{"tiny":1e-10000,"negative":-9223372036854775808}] } `,
			`{"n":9007199254740993,"nested":[{"negative":-9223372036854775808,"tiny":1e-10000}],"x":1.0}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			normalized, err := functionschema.NormalizeObject(json.RawMessage(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if string(normalized) != test.want {
				t.Fatalf("normalized = %s, want %s", normalized, test.want)
			}
			again, err := functionschema.NormalizeObject(normalized)
			if err != nil || string(again) != test.want {
				t.Fatalf("second normalization = %s, %v, want %s", again, err, test.want)
			}
		})
	}
}
