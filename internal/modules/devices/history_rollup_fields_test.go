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

// backfilledHistoryRollupFields reads every frozen field list in the Core
// migrations, delimited by rollup-fields-begin and rollup-fields-end markers.
func backfilledHistoryRollupFields(t *testing.T) map[string]bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "platform", "db", "migrations", "*.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find migrations: %v (%d files)", err, len(paths))
	}
	covered := map[string]bool{}
	goPaths, err := filepath.Glob(filepath.Join("..", "..", "platform", "db", "migration_*.go"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, goPaths...)
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

// missingHistoryRollupBackfills names catalogued numeric fields of types with
// possible history that no migration backfilled.
func missingHistoryRollupBackfills(covered map[string]bool, exempt []string) []string {
	var missing []string
	for typeID := range builtinStateFields {
		for _, path := range HistoryRollupFields(typeID) {
			if key := string(typeID) + " " + path; !covered[key] && !slices.Contains(exempt, key) {
				missing = append(missing, key)
			}
		}
	}
	slices.Sort(missing)
	return missing
}

func TestHistoryRollupBackfillsCoverNumericFields(t *testing.T) {
	t.Parallel()
	covered := backfilledHistoryRollupFields(t)
	if len(covered) == 0 {
		t.Fatal("no rollup backfill field list found")
	}
	if missing := missingHistoryRollupBackfills(covered, historyRollupFieldsWithoutHistory()); len(missing) > 0 {
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

func TestMissingHistoryRollupBackfillsNamesUncoveredField(t *testing.T) {
	t.Parallel()
	covered := backfilledHistoryRollupFields(t)
	delete(covered, "hearth.colorxy/v1 /y")
	got := missingHistoryRollupBackfills(covered, historyRollupFieldsWithoutHistory())
	if !slices.Equal(got, []string{"hearth.colorxy/v1 /y"}) {
		t.Fatalf("missing = %v, want the removed colorxy /y field", got)
	}
	if got = missingHistoryRollupBackfills(covered, []string{"hearth.colorxy/v1 /y"}); len(got) != 0 {
		t.Fatalf("exempt field still reported: %v", got)
	}
	delete(covered, "hearth.colorxy/v1 /x")
	if got = missingHistoryRollupBackfills(
		covered,
		[]string{"hearth.colorxy/v1 /x"},
	); !slices.Equal(
		got,
		[]string{"hearth.colorxy/v1 /y"},
	) {
		t.Fatalf("new /y field must not inherit /x's exemption: %v", got)
	}
}
