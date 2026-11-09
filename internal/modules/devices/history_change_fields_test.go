package devices //nolint:testpackage // Reads the unexported generated field catalog.

import (
	"slices"
	"strings"
	"testing"
)

// historyChangeFieldsWithoutHistory lists the exact "type field" keys present
// when a new Entity type is introduced. Later fields of that type still need
// a backfill, so exempting a whole type would hide missing history.
func historyChangeFieldsWithoutHistory() []string { return nil }

func TestStateChangeBackfillsCoverYesNoAndTextFields(t *testing.T) {
	t.Parallel()
	covered := backfilledFields(t, "change-fields")
	if len(covered) == 0 {
		t.Fatal("no state change backfill field list found")
	}
	var missing []string
	for typeID := range builtinStateFields {
		for _, path := range HistoryChangeFields(typeID) {
			key := string(typeID) + " " + path
			if !covered[key] && !slices.Contains(historyChangeFieldsWithoutHistory(), key) {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		t.Fatalf("yes/no or text fields without a state change backfill: %v; add a backfill migration "+
			"with a change-fields list, or exempt the initial fields of a new type with no history", missing)
	}
	for key := range covered {
		typeID, path, _ := strings.Cut(key, " ")
		if !slices.Contains(HistoryChangeFields(EntityTypeID(typeID)), path) {
			t.Errorf("backfill lists %q, which is not a catalogued yes/no or text field", key)
		}
	}
}
