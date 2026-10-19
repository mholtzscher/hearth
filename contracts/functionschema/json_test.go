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

func FuzzNormalizeObject(f *testing.F) {
	for _, seed := range []string{`{}`, `{"n":9007199254740993,"x":1.0}`, `{"x":1,"x":2}`, `{"x":[{}]}`, `null`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, document string) {
		normalized, err := functionschema.NormalizeObject(json.RawMessage(document))
		if err != nil {
			return
		}
		if !json.Valid(normalized) || len(normalized) > 65536 {
			t.Fatalf("invalid normalized value")
		}
		again, err := functionschema.NormalizeObject(normalized)
		if err != nil || string(again) != string(normalized) {
			t.Fatalf("normalization is not stable")
		}
	})
}
