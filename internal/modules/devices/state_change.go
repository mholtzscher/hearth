package devices

import (
	"context"
	"encoding/json"
	"time"
)

// HistoryChangeFields returns the catalogued yes/no and text field paths for
// typeID. An applied report records a State change for each of these fields
// whose value differs from the Entity's previous State.
func HistoryChangeFields(typeID EntityTypeID) []string {
	var paths []string
	for _, field := range builtinStateFields[typeID] {
		switch field.Value.(type) {
		case BooleanField, StringField:
			paths = append(paths, field.Path)
		}
	}
	return paths
}

// StateChangeRead asks for one field's recorded changes in a range, plus the
// newest change before it. Boolean rejects a text field with value_type.
type StateChangeRead struct {
	EntityID EntityID
	Field    string
	Range    HistoryRange
	Boolean  bool
	// Limit bounds the changes read inside the range; more is query_limit.
	Limit int
}

// StateChangeRow is one recorded change. Value is compact JSON; null means
// the field was absent.
type StateChangeRow struct {
	ObservedAt time.Time
	Value      json.RawMessage
}

// StateChangeRows are in (observed_at, receive_order) order. Seed is the
// newest change before Range.From, or nil when none is retained.
type StateChangeRows struct {
	Seed *StateChangeRow
	Rows []StateChangeRow
}

// StateChangeReader validates the Entity and field and reads one snapshot.
type StateChangeReader interface {
	ReadStateChanges(context.Context, StateChangeRead) (StateChangeRows, error)
}
