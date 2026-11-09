// Package changevalue selects the value a State change records, shared by
// live ingest and the migration backfill so both write identical rows.
package changevalue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Null is the recorded value of an absent field or a known JSON null.
var Null = json.RawMessage("null") //nolint:gochecknoglobals // Immutable literal.

// Select returns the compact JSON value at a root or JSON-pointer field of
// normalized State. An absent field, or a path through a non-object, selects
// null: a missing optional field is a known value, not unknown evidence.
func Select(value json.RawMessage, field string) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var selected any
	if err := decoder.Decode(&selected); err != nil {
		return nil, fmt.Errorf("decode change value: %w", err)
	}
	if field != "" {
		for token := range strings.SplitSeq(strings.TrimPrefix(field, "/"), "/") {
			object, ok := selected.(map[string]any)
			if !ok {
				return Null, nil
			}
			if selected, ok = object[token]; !ok {
				return Null, nil
			}
		}
	}
	encoded, err := json.Marshal(selected)
	if err != nil {
		return nil, fmt.Errorf("encode change value: %w", err)
	}
	return encoded, nil
}
