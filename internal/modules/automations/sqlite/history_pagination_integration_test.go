//go:build integration

package sqlite_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

func TestHistoryPaginationSeeksExclusiveTimeAndID(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	parent, other := newAutomationIDString(t), newAutomationIDString(t)
	at := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	expected := seedHistoryPagination(t, database, parent, other, at)
	params := automations.ListHistoryParams{AutomationID: automations.AutomationID(parent), Limit: 4}
	for offset := 0; ; {
		page, err := repository.ListHistory(t.Context(), params)
		if err != nil {
			t.Fatal(err)
		}
		end := min(offset+params.Limit, len(expected))
		if len(page.Items) != end-offset || page.HasMore != (end < len(expected)) {
			t.Fatalf("page at %d = %#v", offset, page)
		}
		for i, item := range page.Items {
			want := expected[offset+i]
			if item.ID != want.ID || !item.RecordedAt.Equal(want.RecordedAt) ||
				item.AutomationID != params.AutomationID {
				t.Fatalf("item %d = %#v, want %s at %s", offset+i, item, want.ID, want.RecordedAt)
			}
		}
		if !page.HasMore {
			break
		}
		last := page.Items[len(page.Items)-1]
		params.BeforeRecordedAt, params.BeforeID = &last.RecordedAt, &last.ID
		offset = end
	}
	// A cursor is a position, not a lookup. Test non-existent positions for both ID kinds.
	for _, id := range []string{newRunIDString(t), newSkipIDString(t)} {
		params.BeforeRecordedAt, params.BeforeID = &at, &id
		assertHistoryAbsentCursorPage(t, repository, params, expected)
	}
}

func assertHistoryAbsentCursorPage(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	params automations.ListHistoryParams,
	expected []automations.HistorySummary,
) {
	t.Helper()
	page, err := repository.ListHistory(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, item := range expected {
		if item.RecordedAt.Before(*params.BeforeRecordedAt) ||
			(item.RecordedAt.Equal(*params.BeforeRecordedAt) && item.ID < *params.BeforeID) {
			want = append(want, item.ID)
		}
	}
	if page.HasMore != (len(want) > params.Limit) {
		t.Fatalf("absent cursor HasMore = %v for %d entries", page.HasMore, len(want))
	}
	want = want[:min(len(want), params.Limit)]
	var ids []string
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("absent cursor %s = %v, want %v", *params.BeforeID, ids, want)
	}
}

func TestHistoryPaginationContinuesAfterCursorIsPruned(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"run", "skip"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			assertHistoryContinuesAfterPrunedCursor(t, kind)
		})
	}
}

func assertHistoryContinuesAfterPrunedCursor(t *testing.T, kind string) {
	t.Helper()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	parent := newAutomationIDString(t)
	at := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	// The older running Run survives pruning even though the terminal cursor does not.
	olderID := insertRetainedHistoryRun(t, database, parent, "running", at.Add(-time.Nanosecond))
	var cursorID string
	if kind == "run" {
		cursorID = insertRetainedHistoryRun(t, database, parent, "succeeded", at)
	} else {
		cursorID = insertRetainedHistorySkip(t, database, parent, at)
	}
	insertRetainedHistoryRun(t, database, parent, "succeeded", at.Add(time.Nanosecond))
	if count, err := repository.DeleteHistoryBefore(
		t.Context(),
		at.Add(time.Nanosecond),
		500,
	); err != nil ||
		count != 1 {
		t.Fatalf("prune cursor = %d, %v", count, err)
	}
	page, err := repository.ListHistory(t.Context(), automations.ListHistoryParams{
		AutomationID: automations.AutomationID(parent), Limit: 1, BeforeRecordedAt: &at, BeforeID: &cursorID,
	})
	if err != nil || page.HasMore || len(page.Items) != 1 || page.Items[0].ID != olderID {
		t.Fatalf("pruned cursor continuation = %#v, %v", page, err)
	}
	before := at.Add(-time.Nanosecond)
	page, err = repository.ListHistory(t.Context(), automations.ListHistoryParams{
		AutomationID: automations.AutomationID(parent), Limit: 1, BeforeRecordedAt: &before, BeforeID: &olderID,
	})
	if err != nil || page.HasMore || len(page.Items) != 0 {
		t.Fatalf("exhausted continuation = %#v, %v", page, err)
	}
}
