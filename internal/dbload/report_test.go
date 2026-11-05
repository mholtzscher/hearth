package dbload_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/dbload"
)

func TestRenderReportEscapesEmbeddedDataAndKeepsMeasurements(t *testing.T) {
	t.Parallel()

	data := sampleReportData()
	output := filepath.Join(t.TempDir(), "report.html")
	if err := dbload.RenderReport(output, data); err != nil {
		t.Fatalf("RenderReport returned error: %v", err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read rendered report: %v", err)
	}
	html := string(content)
	if !strings.HasPrefix(html, "<!doctype html>") {
		t.Fatalf("report does not start with a doctype")
	}
	if !strings.Contains(html, `id="report-data"`) {
		t.Fatalf("report is missing the embedded data script")
	}
	if !strings.Contains(html, "250 ms for") || !strings.Contains(html, "1000 ms for") {
		t.Fatalf("report is missing the provisional 250 ms/1000 ms gate reference")
	}
	if !strings.Contains(html, `"p95_ms":100`) ||
		!strings.Contains(html, `"ratio":0.5`) ||
		!strings.Contains(html, `"before_min":90`) ||
		!strings.Contains(html, `"errors":0`) ||
		!strings.Contains(html, `"timeouts":0`) {
		t.Fatalf("report data is missing recorded measurements or zero failure counts")
	}
	if strings.Contains(html, "</script><script>window.pwned") ||
		strings.Contains(html, "<script>window.pwned") {
		t.Fatalf("untrusted case or note escaped its data script")
	}
	if !strings.Contains(html, `\u003c/script\u003e`) {
		t.Fatalf("HTML-sensitive bytes were not JSON-escaped in the data script")
	}
	embedded := extractReportData(t, html)
	if len(embedded.Notes) == 0 || !strings.Contains(embedded.Notes[0], "</script>") {
		t.Fatalf("embedded notes did not round-trip: %#v", embedded.Notes)
	}
	if embedded.Blocks[0].P95MS != 100 {
		t.Fatalf("embedded block p95 = %g, want 100", embedded.Blocks[0].P95MS)
	}
	if embedded.Comparisons[0].Ratio != 0.5 {
		t.Fatalf("embedded ratio = %g, want 0.5", embedded.Comparisons[0].Ratio)
	}
	for _, forbidden := range []string{"<script src=", "<link rel=\"stylesheet\"", "@import", "url(http"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("report contains external or inline fetch reference %q", forbidden)
		}
	}
	assertNoNonFiniteMarkers(t, html)
}

func TestRenderReportRejectsNonFiniteMeasurements(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value float64
	}{
		{name: "NaN", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative infinity", value: math.Inf(-1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			data := sampleReportData()
			data.Comparisons[0].Ratio = testCase.value
			output := filepath.Join(t.TempDir(), "report.html")
			err := dbload.RenderReport(output, data)
			if err == nil || !strings.Contains(err.Error(), "encode report data") {
				t.Fatalf("RenderReport error = %v, want non-finite encode failure", err)
			}
			if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
				t.Fatalf("RenderReport left an output file after a non-finite value")
			}
		})
	}
}

func TestRenderReportEmptyEvidenceIsHonest(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "empty.html")
	if err := dbload.RenderReport(output, dbload.ReportData{}); err != nil {
		t.Fatalf("RenderReport returned error: %v", err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read rendered report: %v", err)
	}
	html := string(content)
	if !strings.Contains(html, "Database scale experiment report") {
		t.Fatalf("empty report did not use the neutral title")
	}
	if !strings.Contains(html, "No scenario manifests were recorded.") {
		t.Fatalf("empty report does not disclose missing manifests")
	}
	if strings.Contains(html, `"p95_ms":`) {
		t.Fatalf("empty report appears to contain fabricated block measurements")
	}
	if !strings.Contains(html, "Missing values are shown as gaps or dashes, never as zero latency.") {
		t.Fatalf("empty report does not state how missing values are shown")
	}
	assertNoNonFiniteMarkers(t, html)
}

func TestRenderReportReportsOutputPathFailure(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "missing", "report.html")
	if err := dbload.RenderReport(output, dbload.ReportData{}); err == nil {
		t.Fatalf("RenderReport succeeded for a missing output directory")
	}
}

func TestRenderReportKeepsCoverageRegistrationsAndNotes(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "report.html")
	if err := dbload.RenderReport(output, sampleReportData()); err != nil {
		t.Fatalf("RenderReport returned error: %v", err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read rendered report: %v", err)
	}
	embedded := extractReportData(t, string(content))
	if len(embedded.Coverage) != 2 {
		t.Fatalf("coverage entries = %d, want 2", len(embedded.Coverage))
	}
	if embedded.Coverage[1].Status != "uncovered" ||
		!strings.Contains(embedded.Coverage[1].Reason, "no representative") {
		t.Fatalf("uncovered reason did not round-trip: %#v", embedded.Coverage[1])
	}
	if len(embedded.Registrations) != 1 || embedded.Registrations[0].Pairs != 20 {
		t.Fatalf("registration did not round-trip: %#v", embedded.Registrations)
	}
	if len(embedded.Comparisons) != 2 || embedded.Comparisons[1].Verdict != "inconclusive" ||
		embedded.Comparisons[1].Reason == "" {
		t.Fatalf("inconclusive comparison did not round-trip: %#v", embedded.Comparisons)
	}
	if len(embedded.Artifacts) != 1 || !strings.Contains(embedded.Artifacts[0], ".data/dbload") {
		t.Fatalf("artifact path did not round-trip: %#v", embedded.Artifacts)
	}
	if embedded.Environment["host.load"] != "0.4" {
		t.Fatalf("environment did not round-trip: %#v", embedded.Environment)
	}
	if len(embedded.Manifests) != 1 || embedded.Manifests[0].RowCounts["devices"] != 10 {
		t.Fatalf("manifest row counts did not round-trip: %#v", embedded.Manifests)
	}
}

func sampleReportData() dbload.ReportData {
	unsafe := `</script><script>window.pwned=1</script>`
	return dbload.ReportData{
		Title:       "Database scale report </title><script>window.pwned=1</script>",
		GeneratedAt: time.Date(2026, time.November, 4, 12, 30, 0, 0, time.UTC),
		Environment: map[string]string{
			"host.load":  "0.4",
			"go.version": "go1.27.1",
		},
		Manifests: []dbload.Manifest{
			{
				FormatVersion: 1,
				SuiteRevision: "suite-rev",
				Scenario: dbload.Scenario{
					Name:        "population-10x",
					Dimension:   "population",
					Scale:       10,
					Concurrency: 1,
					Seed:        1,
				},
				Clock:        time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC),
				RowCounts:    map[string]int64{"devices": 10, "entities": 40},
				FixtureHash:  "fixture-hash",
				PhysicalHash: "physical-hash",
				Database:     ".data/dbload/campaign/population-10x.db",
				HistoryRows:  1200,
				Limits:       []string{"fan-out capped at legal maximum 64"},
			},
		},
		Blocks: []dbload.Block{
			{
				Case: "entity-get", Scenario: "population-10x", Variant: "baseline", Pair: 1,
				Order: "AB", Concurrency: 1, Attempted: 200, Completed: 200, Work: 200,
				P50MS: 60, P95MS: 100, P99MS: 140, WallMS: 1500, PoolWaitMS: 2,
				Status: "complete",
			},
			{
				Case: "entity-get", Scenario: "population-10x", Variant: "candidate", Pair: 1,
				Order: "BA", Concurrency: 1, Attempted: 200, Completed: 200, Work: 200,
				P50MS: 30, P95MS: 50, P99MS: 70, WallMS: 800, PoolWaitMS: 0,
				Status: "complete",
			},
			{
				Case: "unsafe-case-" + unsafe, Scenario: "population-10x", Variant: "baseline", Pair: 1,
				Order: "AB", Concurrency: 1, Attempted: 200, Completed: 199, Work: 199,
				P95MS: 0, Status: "failed",
			},
		},
		Comparisons: []dbload.Comparison{
			{
				Case: "entity-get", Pairs: 20, Ratio: 0.5, Lower: 0.4, Upper: 0.6,
				PValue: 0.00001, AdjustedP: 0.00002, BeforeMS: 100, BeforeMin: 90, BeforeMax: 110,
				AfterMS: 50, AfterMin: 45, AfterMax: 55, Verdict: "improved",
				Reason: "candidate p95 geometric mean is 50.0% lower",
			},
			{
				Case: "unsafe-" + unsafe, Verdict: "inconclusive",
				Reason: "no recorded blocks for registered cell",
			},
		},
		Registrations: []dbload.Registration{
			{
				FormatVersion: 1, Slot: 1, BaselineHash: "baseline-hash", CandidateHash: "candidate-hash",
				SuiteHash: "suite-hash", FixtureHash: "fixture-hash", ScheduleHash: "schedule-hash",
				Cells: []string{"entity-get", "unsafe-" + unsafe}, Pairs: 20, SamplesPerCell: 200,
				MinimumGain: 0.1, Alpha: 0.05 / 3, ContentHash: "content-hash", ResamplingSeed: 424242,
			},
		},
		Coverage: []dbload.QueryCoverage{
			{
				Owner:  "devices",
				Name:   "GetEntity",
				Source: "devices/sqlite/query.sql",
				Hash:   "abc",
				Cases:  []string{"entity-get"},
				Status: "covered",
			},
			{
				Owner:  "devices",
				Name:   "ListEntities",
				Source: "devices/sqlite/query.sql",
				Hash:   "def",
				Status: "uncovered",
				Reason: "no representative sparse-match fixture",
			},
		},
		Notes: []string{
			"Attempted index candidate reverted after write regression. " + unsafe,
			"Host load was recorded before each pair.",
		},
		Artifacts: []string{".data/dbload/campaign/observations.jsonl"},
	}
}

func extractReportData(t *testing.T, html string) dbload.ReportData {
	t.Helper()
	const marker = `<script type="application/json" id="report-data">`
	start := strings.Index(html, marker)
	if start < 0 {
		t.Fatalf("embedded report data marker not found")
	}
	start += len(marker)
	end := strings.Index(html[start:], "</script>")
	if end < 0 {
		t.Fatalf("embedded report data terminator not found")
	}
	var data dbload.ReportData
	if err := json.Unmarshal([]byte(html[start:start+end]), &data); err != nil {
		t.Fatalf("decode embedded report data: %v", err)
	}
	return data
}

func assertNoNonFiniteMarkers(t *testing.T, html string) {
	t.Helper()
	nonFinite := regexp.MustCompile(`\b(NaN|Inf|Infinity)\b`)
	if match := nonFinite.FindString(html); match != "" {
		t.Fatalf("report contains non-finite marker %q", match)
	}
}
