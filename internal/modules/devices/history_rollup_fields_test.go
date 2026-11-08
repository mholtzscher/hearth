package devices //nolint:testpackage // Reads the unexported generated field catalog.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// historyRollupFieldsWithoutHistory lists the exact "type field" keys present
// when a new Entity type is introduced. Later fields of that type still need
// a backfill, so exempting a whole type would hide missing history.
func historyRollupFieldsWithoutHistory() []string { return nil }

var historyRollupFieldRow = regexp.MustCompile(`\('([^']+)', '([^']*)'\)`)

// backfilledHistoryRollupFields reads every frozen field list in the Go
// migrations, delimited by rollup-fields-begin and rollup-fields-end markers.
func backfilledHistoryRollupFields(t *testing.T) map[string]bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "platform", "db", "migration_*.go"))
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, path := range paths {
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		_, rest, found := strings.Cut(string(source), "-- rollup-fields-begin")
		if !found {
			continue
		}
		list, _, closed := strings.Cut(rest, "-- rollup-fields-end")
		if !closed {
			t.Fatalf("%s: rollup-fields-begin without rollup-fields-end", path)
		}
		for _, match := range historyRollupFieldRow.FindAllStringSubmatch(list, -1) {
			covered[match[1]+" "+match[2]] = true
		}
	}
	return covered
}

func TestHistoryRollupBackfillsCoverNumericFields(t *testing.T) {
	t.Parallel()
	covered := backfilledHistoryRollupFields(t)
	if len(covered) == 0 {
		t.Fatal("no rollup backfill field list found")
	}
	var missing []string
	for typeID := range builtinStateFields {
		for _, path := range HistoryRollupFields(typeID) {
			key := string(typeID) + " " + path
			if !covered[key] && !slices.Contains(historyRollupFieldsWithoutHistory(), key) {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		t.Fatalf("numeric fields without a rollup backfill: %v; add a backfill migration "+
			"with a rollup-fields list, or exempt the initial fields of a new type with no history", missing)
	}
	for key := range covered {
		typeID, path, _ := strings.Cut(key, " ")
		if !slices.Contains(HistoryRollupFields(EntityTypeID(typeID)), path) {
			t.Errorf("backfill lists %q, which is not a catalogued numeric field", key)
		}
	}
}
