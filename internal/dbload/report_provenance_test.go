package dbload_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/dbload"
)

func TestReportRejectsIncompatibleSuiteFixtureOrEnvironment(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"suite", "fixture", "environment", "format"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			first := dbload.ReportData{
				FormatVersion: 1,
				SuiteHash:     "suite",
				Environment:   map[string]string{"go": "go1.27.1"},
				Manifests: []dbload.Manifest{
					{Scenario: dbload.Scenario{Name: "population-1"}, FixtureHash: "logical"},
				},
			}
			second := first
			second.Environment = map[string]string{"go": "go1.27.1"}
			second.Manifests = append([]dbload.Manifest{}, first.Manifests...)
			switch change {
			case "suite":
				second.SuiteHash = "different"
			case "fixture":
				second.Manifests[0].FixtureHash = "different"
			case "environment":
				second.Environment["go"] = "different"
			case "format":
				second.FormatVersion = 999
			}
			a := filepath.Join(root, "a.json")
			b := filepath.Join(root, "b.json")
			require.NoError(t, dbload.WriteJSON(a, first))
			require.NoError(t, dbload.WriteJSON(b, second))
			require.Error(t, dbload.Report([]string{a, b}, filepath.Join(root, "report.html")))
		})
	}
}

func TestReportPreservesDistinctQueryVersionsAndSourceEnvironments(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := dbload.ReportData{
		FormatVersion: 1,
		SuiteHash:     "suite",
		Environment:   map[string]string{"go": "go1.27.1", "vcs.revision": "before"},
		Coverage: []dbload.QueryCoverage{
			{Owner: "agent", Name: "ListConversationSummaries", Hash: "old", Status: "timed"},
		},
	}
	second := dbload.ReportData{
		FormatVersion: 1,
		SuiteHash:     "suite",
		Environment:   map[string]string{"go": "go1.27.1", "vcs.revision": "after"},
		Coverage: []dbload.QueryCoverage{
			{Owner: "agent", Name: "ListConversationSummaries", Hash: "new", Status: "timed"},
		},
	}
	a := filepath.Join(root, "a.json")
	b := filepath.Join(root, "b.json")
	output := filepath.Join(root, "report.html")
	require.NoError(t, dbload.WriteJSON(a, first))
	require.NoError(t, dbload.WriteJSON(b, second))
	require.NoError(t, dbload.Report([]string{a, b}, output))
	raw, err := os.ReadFile(output)
	require.NoError(t, err)
	data := extractReportData(t, string(raw))
	require.Len(t, data.Coverage, 2)
	require.Contains(t, data.Environment["input "+a], "before")
	require.Contains(t, data.Environment["input "+b], "after")
}
