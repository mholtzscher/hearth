package devices //nolint:testpackage // Reads the unexported generated field catalog.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// historyRollupTypesWithoutHistory lists Entity types introduced after the
// latest rollup backfill. Their reports always arrive through ingest, which
// builds cells, so no backfill is needed. Add a type here only in the change
// that introduces it.
func historyRollupTypesWithoutHistory() []EntityTypeID { return nil }

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
func missingHistoryRollupBackfills(covered map[string]bool, exempt []EntityTypeID) []string {
	var missing []string
	for typeID := range builtinStateFields {
		if slices.Contains(exempt, typeID) {
			continue
		}
		for _, path := range HistoryRollupFields(typeID) {
			if key := string(typeID) + " " + path; !covered[key] {
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
	if missing := missingHistoryRollupBackfills(covered, historyRollupTypesWithoutHistory()); len(missing) > 0 {
		t.Fatalf("numeric fields without a rollup backfill: %v; add a backfill migration "+
			"with a rollup-fields list, or exempt a type that has no retained history", missing)
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
	got := missingHistoryRollupBackfills(covered, historyRollupTypesWithoutHistory())
	if !slices.Equal(got, []string{"hearth.colorxy/v1 /y"}) {
		t.Fatalf("missing = %v, want the removed colorxy /y field", got)
	}
	if got = missingHistoryRollupBackfills(covered, []EntityTypeID{EntityTypeColorxyV1}); len(got) != 0 {
		t.Fatalf("exempt type still reported: %v", got)
	}
}
