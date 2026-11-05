// Package dbload runs reproducible, isolated database scale experiments.
// It is developer tooling, not a production service.
package dbload

import "time"

// Scenario isolates one growth dimension while preserving domain limits.
type Scenario struct {
	Name        string `json:"name"`
	Dimension   string `json:"dimension"`
	Scale       int    `json:"scale"`
	Relation    string `json:"relation,omitempty"`
	Concurrency int    `json:"concurrency"`
	Seed        int64  `json:"seed"`
}

// Manifest identifies a closed fixture and its independently checked contents.
type Manifest struct {
	FormatVersion  int              `json:"format_version"`
	SuiteRevision  string           `json:"suite_revision"`
	Scenario       Scenario         `json:"scenario"`
	Clock          time.Time        `json:"clock"`
	RowCounts      map[string]int64 `json:"row_counts"`
	FixtureHash    string           `json:"fixture_hash"`
	PhysicalHash   string           `json:"physical_hash"`
	Database       string           `json:"database"`
	EntityID       string           `json:"entity_id"`
	EventEntityID  string           `json:"event_entity_id"`
	DeviceID       string           `json:"device_id"`
	AdapterID      string           `json:"adapter_id"`
	RuntimeID      string           `json:"runtime_id"`
	AutomationID   string           `json:"automation_id"`
	ConversationID string           `json:"conversation_id"`
	DestinationID  string           `json:"destination_id"`
	FunctionName   string           `json:"function_name"`
	DashboardID    string           `json:"dashboard_id"`
	HistoryRows    int              `json:"history_rows"`
	Limits         []string         `json:"limits,omitempty"`
}

// Observation records every attempt, including failed and censored work.
type Observation struct {
	Case         string `json:"case"`
	ParameterSet string `json:"parameter_set"`
	Concurrency  int    `json:"concurrency"`
	Pair         int    `json:"pair"`
	PairOrder    string `json:"pair_order"`
	Block        int    `json:"block"`
	Variant      string `json:"variant"`
	Sequence     int    `json:"sequence"`
	ElapsedNS    int64  `json:"elapsed_ns"`
	Work         int64  `json:"work"`
	ErrorClass   string `json:"error_class,omitempty"`
	TimedOut     bool   `json:"timed_out"`
	Correct      bool   `json:"correct"`
}

// Block is the experimental unit; its requests are not independent replications.
type Block struct {
	Case        string  `json:"case"`
	Scenario    string  `json:"scenario"`
	Variant     string  `json:"variant"`
	Pair        int     `json:"pair"`
	Order       string  `json:"order"`
	Concurrency int     `json:"concurrency"`
	Attempted   int     `json:"attempted"`
	Completed   int     `json:"completed"`
	Errors      int     `json:"errors"`
	Timeouts    int     `json:"timeouts"`
	Work        int64   `json:"work"`
	P50MS       float64 `json:"p50_ms"`
	P95MS       float64 `json:"p95_ms"`
	P99MS       float64 `json:"p99_ms"`
	WallMS      float64 `json:"wall_ms"`
	PoolWaitMS  float64 `json:"pool_wait_ms"`
	Status      string  `json:"status"`
}

// Registration freezes one confirmation family before its first measurement.
type Registration struct {
	FormatVersion  int      `json:"format_version"`
	Slot           int      `json:"slot"`
	BaselineHash   string   `json:"baseline_hash"`
	CandidateHash  string   `json:"candidate_hash"`
	SuiteHash      string   `json:"suite_hash"`
	FixtureHash    string   `json:"fixture_hash"`
	ScheduleHash   string   `json:"schedule_hash"`
	Cells          []string `json:"cells"`
	Pairs          int      `json:"pairs"`
	SamplesPerCell int      `json:"samples_per_cell"`
	MinimumGain    float64  `json:"minimum_gain"`
	Alpha          float64  `json:"alpha"`
	ContentHash    string   `json:"content_hash"`
	ResamplingSeed int64    `json:"resampling_seed"`
}

// Comparison distinguishes statistical evidence, practical size, and gate status.
type Comparison struct {
	Case      string  `json:"case"`
	Pairs     int     `json:"pairs"`
	Ratio     float64 `json:"ratio"`
	Lower     float64 `json:"lower"`
	Upper     float64 `json:"upper"`
	PValue    float64 `json:"p_value"`
	AdjustedP float64 `json:"adjusted_p"`
	BeforeMS  float64 `json:"before_ms"`
	AfterMS   float64 `json:"after_ms"`
	BeforeMin float64 `json:"before_min"`
	BeforeMax float64 `json:"before_max"`
	AfterMin  float64 `json:"after_min"`
	AfterMax  float64 `json:"after_max"`
	Verdict   string  `json:"verdict"`
	Reason    string  `json:"reason,omitempty"`
}

// QueryCoverage records query inventory separately from measured operation coverage.
type QueryCoverage struct {
	Owner  string   `json:"owner"`
	Name   string   `json:"name"`
	Source string   `json:"source"`
	Hash   string   `json:"hash"`
	Cases  []string `json:"cases,omitempty"`
	Status string   `json:"status"`
	Reason string   `json:"reason,omitempty"`
}

// ReportData is the offline report's only data source.
type ReportData struct {
	Title         string            `json:"title"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Environment   map[string]string `json:"environment"`
	Manifests     []Manifest        `json:"manifests"`
	Blocks        []Block           `json:"blocks"`
	Comparisons   []Comparison      `json:"comparisons"`
	Registrations []Registration    `json:"registrations"`
	Coverage      []QueryCoverage   `json:"coverage"`
	Notes         []string          `json:"notes"`
	Artifacts     []string          `json:"artifacts"`
}
