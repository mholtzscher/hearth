package agenteval

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// WorktreeRef builds the driver's own checkout, including uncommitted changes.
const WorktreeRef = "worktree"

const (
	// maxConsecutiveProviderErrors stops a run whose provider keeps failing.
	maxConsecutiveProviderErrors = 3
	// shortRevisionLength is how much of a commit the log shows.
	shortRevisionLength = 12
	// maxTrialLineBytes bounds one trials.jsonl line.
	maxTrialLineBytes = 256 << 20
	// assumedModelSeconds estimates each request's model time in preflight.
	assumedModelSeconds = 6
)

// Arm is one side of a comparison.
type Arm struct {
	Name     string        `json:"name"`
	Ref      string        `json:"ref"`
	Revision string        `json:"revision"`
	Settings ModelSettings `json:"model"`
	binary   string
}

// RunOptions configures a comparison or a single-arm run.
type RunOptions struct {
	Repo             string
	Arms             []Arm // one arm for run, baseline then candidate for compare
	ScenarioDir      string
	SimulatorConfig  string
	Tags             []string
	Trials           int
	TrialMaxRequests int
	RunMaxRequests   int
	MinGap           time.Duration
	RateLimitPause   time.Duration
	Out              string
	Resume           bool
	Rule             Rule
	Log              io.Writer
	// Judge optionally grades rubrics; its grades are report-only.
	Judge *Judge
}

// Manifest pins what a run measured, so resume can refuse a changed setup.
type Manifest struct {
	Protocol         int                `json:"protocol"`
	Arms             []Arm              `json:"arms"`
	Scenarios        []ManifestScenario `json:"scenarios"`
	SimulatorSHA     string             `json:"simulator_sha256"`
	Trials           int                `json:"trials"`
	TrialMaxRequests int                `json:"trial_max_requests"`
	RunMaxRequests   int                `json:"run_max_requests"`
	MinGapSeconds    float64            `json:"min_gap_seconds"`
	Rule             Rule               `json:"rule"`
	Judge            *ModelSettings     `json:"judge,omitempty"`
	StartedAt        time.Time          `json:"started_at"`
}

// ManifestScenario identifies one scenario file in a run.
type ManifestScenario struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Tags   []string `json:"tags"`
	SHA256 string   `json:"sha256"`
}

// ArmTrial is one scored trial in trials.jsonl.
type ArmTrial struct {
	Arm      string      `json:"arm"`
	Trial    int         `json:"trial"`
	Scored   ScoredTrial `json:"scored"`
	Judgment *Judgment   `json:"judgment,omitempty"`
}

func (trial ArmTrial) key() string {
	return trialKey(trial.Arm, trial.Scored.Record.ScenarioID, trial.Trial)
}

func trialKey(arm, scenario string, trial int) string {
	return arm + "/" + scenario + "/" + strconv.Itoa(trial)
}

// Run builds each arm, runs the schedule, and writes manifest.json,
// trials.jsonl, records/, and summary.json under Out.
func Run(ctx context.Context, options RunOptions) (Summary, error) {
	logf := func(format string, args ...any) { _, _ = fmt.Fprintf(options.Log, format+"\n", args...) }
	scenarios, err := LoadScenarios(options.ScenarioDir)
	if err != nil {
		return Summary{}, err
	}
	scenarios = filterTags(scenarios, options.Tags)
	if len(scenarios) == 0 {
		return Summary{}, errors.New("no scenarios match the selected tags")
	}
	if options.Judge != nil {
		if err = CheckJudgeModel(options.Judge.Settings, options.Arms); err != nil {
			return Summary{}, err
		}
	}
	if err = os.MkdirAll(filepath.Join(options.Out, "records"), 0o750); err != nil {
		return Summary{}, err
	}
	for index := range options.Arms {
		if err = buildArm(ctx, options.Repo, options.Out, &options.Arms[index], logf); err != nil {
			return Summary{}, err
		}
	}
	manifest, err := newManifest(options, scenarios)
	if err != nil {
		return Summary{}, err
	}
	done, used, err := prepareOutput(options, manifest)
	if err != nil {
		return Summary{}, err
	}
	preflight(scenarios, options, logf)
	trialsFile, err := os.OpenFile(
		filepath.Join(options.Out, "trials.jsonl"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return Summary{}, err
	}
	defer func() { _ = trialsFile.Close() }()
	schedule := &schedule{options: options, done: done, trials: trialsFile, logf: logf}
	schedule.used = used
	if err = schedule.run(ctx, scenarios); err != nil {
		return Summary{}, err
	}
	summary := Summarize(
		scenarios,
		slices.Collect(maps.Values(done)),
		options.Rule,
		schedule.capReached,
		schedule.notRun,
	)
	raw, _ := json.MarshalIndent(summary, "", "  ")
	if err = os.WriteFile(filepath.Join(options.Out, "summary.json"), raw, 0o600); err != nil {
		return summary, err
	}
	return summary, nil
}

// schedule runs the remaining trials in alternating arm order and tracks
// the run-level request budget.
type schedule struct {
	options              RunOptions
	done                 map[string]ArmTrial
	trials               io.Writer
	logf                 func(string, ...any)
	used                 int
	capReached           bool
	notRun               int
	consecutiveProviders int
}

func (s *schedule) run(ctx context.Context, scenarios []Scenario) error {
	for trial := range s.options.Trials {
		for scenarioIndex, scenario := range scenarios {
			order := slices.Clone(s.options.Arms)
			if (trial+scenarioIndex)%2 == 1 {
				slices.Reverse(order)
			}
			for _, arm := range order {
				if err := s.step(ctx, arm, scenario, trial); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// step runs one arm's trial unless it is already done or the run cap would
// be exceeded.
func (s *schedule) step(ctx context.Context, arm Arm, scenario Scenario, trial int) error {
	key := trialKey(arm.Name, scenario.ID, trial)
	// Incomplete trials from an earlier attempt run again; the retry replaces them.
	if previous, ok := s.done[key]; ok && previous.Scored.Scored {
		return nil
	}
	perTrial := scenario.MaxRequests
	if perTrial == 0 {
		perTrial = s.options.TrialMaxRequests
	}
	// Budgeting each trial at its cap, not its expected use, keeps the run
	// within RunMaxRequests even when every remaining trial uses its whole cap.
	if s.capReached || (s.options.RunMaxRequests > 0 && s.used+perTrial > s.options.RunMaxRequests) {
		s.capReached = true
		s.notRun++
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := runWorker(ctx, s.options, arm, scenario, trial, perTrial)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	s.used += len(record.Requests)
	if record.Protocol != WorkerProtocol {
		return fmt.Errorf("%s: worker protocol %d, driver %d", key, record.Protocol, WorkerProtocol)
	}
	if record.ScenarioSHA != scenario.SHA256 {
		return fmt.Errorf("%s: worker read a different scenario file", key)
	}
	scored := ArmTrial{Arm: arm.Name, Trial: trial, Scored: Score(scenario, record)}
	if scored.Judgment, err = s.judge(ctx, arm, scenario, record); err != nil {
		return err
	}
	scored.Scored.Record = slim(scored.Scored.Record)
	line, _ := json.Marshal(scored)
	if _, err = s.trials.Write(append(line, '\n')); err != nil {
		return err
	}
	s.done[key] = scored
	s.logf("%-9s %-32s trial %2d  %-15s %s", arm.Name, scenario.ID, trial+1, record.Outcome, passLabel(scored))
	if s.consecutiveProviders, err = afterProviderError(
		ctx, record, s.consecutiveProviders, s.options, s.logf,
	); err != nil {
		return err
	}
	return sleep(ctx, s.options.MinGap)
}

// judge grades a completed trial when a judge and rubric are configured. It
// paces the request like an Agent request when both share a provider.
func (s *schedule) judge(ctx context.Context, arm Arm, scenario Scenario, record TrialRecord) (*Judgment, error) {
	if s.options.Judge == nil {
		return nil, nil //nolint:nilnil // No judge means no judgment, which is not an error.
	}
	input, ok := JudgeInputFor(scenario, record)
	if !ok {
		return nil, nil //nolint:nilnil // Unjudged trials carry no judgment.
	}
	if s.options.Judge.Settings.Provider == arm.Settings.Provider {
		if err := sleep(ctx, s.options.MinGap); err != nil {
			return nil, err
		}
	}
	judgment := s.options.Judge.Grade(ctx, input)
	return &judgment, ctx.Err()
}

// afterProviderError stops on a rejected credential or repeated failures,
// pauses after a rate limit, and otherwise resets the failure count.
func afterProviderError(
	ctx context.Context, record TrialRecord, consecutive int, options RunOptions, logf func(string, ...any),
) (int, error) {
	if record.Outcome != TrialProviderError {
		return 0, nil
	}
	consecutive++
	switch {
	case strings.Contains(record.Error, "http_401") || strings.Contains(record.Error, "http_403"):
		return consecutive, errors.New("provider rejected the credential; stopping")
	case consecutive >= maxConsecutiveProviderErrors:
		return consecutive, errors.New("repeated provider errors; stopping (continue later with --resume)")
	case strings.Contains(record.Error, "http_429"):
		logf("rate limited; pausing %s", options.RateLimitPause)
		return consecutive, sleep(ctx, options.RateLimitPause)
	}
	return consecutive, nil
}

func passLabel(trial ArmTrial) string {
	switch {
	case !trial.Scored.Scored:
		return "incomplete"
	case trial.Scored.Passed:
		return "pass"
	case trial.Scored.SafetyFailed():
		return "FAIL (safety)"
	default:
		return "fail"
	}
}

func filterTags(scenarios []Scenario, tags []string) []Scenario {
	if len(tags) == 0 {
		return scenarios
	}
	return slices.DeleteFunc(slices.Clone(scenarios), func(scenario Scenario) bool {
		return !slices.ContainsFunc(
			tags,
			func(tag string) bool { return slices.Contains(scenario.Tags, tag) || tag == scenario.ID },
		)
	})
}

func sleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// buildArm resolves the arm's revision and builds its worker binary.
func buildArm(ctx context.Context, repo, out string, arm *Arm, logf func(string, ...any)) error {
	source := repo
	if arm.Ref == WorktreeRef {
		revision, err := worktreeRevision(ctx, repo)
		if err != nil {
			return err
		}
		arm.Revision = revision
	} else {
		sha, err := git(ctx, repo, "rev-parse", "--verify", arm.Ref+"^{commit}")
		if err != nil {
			return fmt.Errorf("resolve %s: %w", arm.Ref, err)
		}
		arm.Revision = sha
		source = filepath.Join(out, "src", arm.Name)
		_ = os.RemoveAll(source)
		if _, err = git(ctx, repo, "worktree", "add", "--detach", source, sha); err != nil {
			return fmt.Errorf("check out %s: %w", arm.Ref, err)
		}
		defer func() { _, _ = git(context.WithoutCancel(ctx), repo, "worktree", "remove", "--force", source) }()
	}
	binary, err := filepath.Abs(filepath.Join(out, "bin", arm.Name))
	if err != nil {
		return err
	}
	logf("building %s worker from %s (%s)", arm.Name, arm.Ref, short(arm.Revision))
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, "./internal/cmd/agenteval")
	command.Dir = source
	if output, buildErr := command.CombinedOutput(); buildErr != nil {
		return fmt.Errorf("build %s: %w\n%s", arm.Ref, buildErr, output)
	}
	arm.binary = binary
	protocol, err := exec.CommandContext(ctx, binary, "protocol").Output()
	if err != nil {
		return fmt.Errorf("%s has no agenteval worker protocol: %w", arm.Ref, err)
	}
	var reported struct {
		Protocol int `json:"protocol"`
	}
	if err = json.Unmarshal(protocol, &reported); err != nil {
		return fmt.Errorf("%s worker protocol output: %w", arm.Ref, err)
	}
	if reported.Protocol != WorkerProtocol {
		return fmt.Errorf("%s worker protocol %d, driver %d", arm.Ref, reported.Protocol, WorkerProtocol)
	}
	return nil
}

func short(revision string) string {
	head, dirty, _ := strings.Cut(revision, "+")
	if len(head) > shortRevisionLength {
		head = head[:shortRevisionLength]
	}
	if dirty != "" {
		return head + "+" + dirty
	}
	return head
}

// worktreeRevision is HEAD plus a hash of uncommitted and untracked changes.
func worktreeRevision(ctx context.Context, repo string) (string, error) {
	head, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	status, err := git(ctx, repo, "status", "--porcelain")
	if err != nil || status == "" {
		return head, err
	}
	diff, err := git(ctx, repo, "diff", "HEAD")
	if err != nil {
		return "", err
	}
	untracked, err := git(ctx, repo, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, diff)
	for name := range strings.SplitSeq(untracked, "\x00") {
		if name == "" {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(repo, name))
		if readErr != nil {
			return "", fmt.Errorf("hash untracked %s: %w", name, readErr)
		}
		_, _ = io.WriteString(hash, name)
		_, _ = hash.Write(content)
	}
	return head + "+dirty:" + hex.EncodeToString(hash.Sum(nil))[:12], nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func newManifest(options RunOptions, scenarios []Scenario) (Manifest, error) {
	raw, err := os.ReadFile(options.SimulatorConfig)
	if err != nil {
		return Manifest{}, err
	}
	sum := sha256.Sum256(raw)
	manifest := Manifest{
		Protocol: WorkerProtocol, Arms: options.Arms,
		SimulatorSHA: hex.EncodeToString(sum[:]), Trials: options.Trials,
		TrialMaxRequests: options.TrialMaxRequests, RunMaxRequests: options.RunMaxRequests,
		MinGapSeconds: options.MinGap.Seconds(), Rule: options.Rule, StartedAt: time.Now().UTC(),
	}
	if options.Judge != nil {
		manifest.Judge = &options.Judge.Settings
	}
	for _, scenario := range scenarios {
		manifest.Scenarios = append(manifest.Scenarios, ManifestScenario{
			ID: scenario.ID, Title: scenario.Title, Tags: scenario.Tags, SHA256: scenario.SHA256,
		})
	}
	return manifest, nil
}

// prepareOutput writes a new manifest, or on resume verifies the existing one
// and returns the trials already recorded and the model requests they used.
func prepareOutput(options RunOptions, manifest Manifest) (map[string]ArmTrial, int, error) {
	path := filepath.Join(options.Out, "manifest.json")
	done := map[string]ArmTrial{}
	if !options.Resume {
		if _, err := os.Stat(path); err == nil {
			return nil, 0, fmt.Errorf("%s already holds a run; use --resume or a new --out", options.Out)
		}
		raw, _ := json.MarshalIndent(manifest, "", "  ")
		return done, 0, os.WriteFile(path, raw, 0o600)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("resume: %w", err)
	}
	var previous Manifest
	if err = json.Unmarshal(raw, &previous); err != nil {
		return nil, 0, fmt.Errorf("resume: %w", err)
	}
	if reason := sameSetup(previous, manifest); reason != "" {
		return nil, 0, fmt.Errorf("resume refused: %s changed", reason)
	}
	trials, used, err := readTrials(filepath.Join(options.Out, "trials.jsonl"))
	for _, trial := range trials {
		done[trial.key()] = trial
	}
	return done, used, err
}

// readTrials reads trials.jsonl for resume, keeping the last line for each
// trial, and returns the model requests every line used. An interrupted
// write can leave a partial last line; it is truncated so appends start on a
// fresh line.
func readTrials(path string) ([]ArmTrial, int, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if complete := bytes.LastIndexByte(raw, '\n') + 1; complete < len(raw) {
		if err = os.Truncate(path, int64(complete)); err != nil {
			return nil, 0, err
		}
		raw = raw[:complete]
	}
	var trials []ArmTrial
	used := 0
	for line := range bytes.Lines(raw) {
		var trial ArmTrial
		if err = json.Unmarshal(line, &trial); err != nil {
			return nil, 0, fmt.Errorf("%s: %w", path, err)
		}
		used += len(trial.Scored.Record.Requests)
		trials = append(trials, trial)
	}
	return latestTrials(trials), used, nil
}

// latestTrials keeps the last line for each trial, in first-seen order: a
// resumed run's retry replaces the incomplete attempt it repeats.
func latestTrials(trials []ArmTrial) []ArmTrial {
	positions := map[string]int{}
	latest := make([]ArmTrial, 0, len(trials))
	for _, trial := range trials {
		if position, ok := positions[trial.key()]; ok {
			latest[position] = trial
			continue
		}
		positions[trial.key()] = len(latest)
		latest = append(latest, trial)
	}
	return latest
}

func sameSetup(a, b Manifest) string {
	switch {
	case a.Protocol != b.Protocol:
		return "worker protocol"
	case len(a.Arms) != len(b.Arms):
		return "arms"
	case a.SimulatorSHA != b.SimulatorSHA:
		return "household fixture"
	case a.Trials != b.Trials:
		return "trial count"
	case (a.Judge == nil) != (b.Judge == nil) || (a.Judge != nil && *a.Judge != *b.Judge):
		return "judge model"
	}
	for index := range a.Arms {
		if a.Arms[index].Revision != b.Arms[index].Revision {
			return a.Arms[index].Name + " revision"
		}
		if a.Arms[index].Settings != b.Arms[index].Settings {
			return a.Arms[index].Name + " model settings"
		}
	}
	if !slices.EqualFunc(a.Scenarios, b.Scenarios, func(x, y ManifestScenario) bool {
		return x.ID == y.ID && x.SHA256 == y.SHA256
	}) {
		return "scenario files"
	}
	return ""
}

func preflight(scenarios []Scenario, options RunOptions, logf func(string, ...any)) {
	perTrial := 0
	for _, scenario := range scenarios {
		perTrial += len(scenario.Reference)
	}
	requests := perTrial * options.Trials * len(options.Arms)
	seconds := float64(requests) * (options.MinGap.Seconds() + assumedModelSeconds)
	logf(
		"%d scenarios × %d trials × %d arm(s): about %d model requests (reference trajectories), roughly %s at a %s gap",
		len(scenarios),
		options.Trials,
		len(options.Arms),
		requests,
		(time.Duration(seconds) * time.Second).Round(time.Minute),
		options.MinGap,
	)
	if options.RunMaxRequests > 0 {
		logf("run cap: %d model requests", options.RunMaxRequests)
	}
}

// runWorker runs one trial in a separate worker process at the arm's revision.
func runWorker(
	ctx context.Context, options RunOptions, arm Arm, scenario Scenario, trial, maxRequests int,
) (TrialRecord, error) {
	path, err := scenarioPath(options.ScenarioDir, scenario.ID)
	if err != nil {
		return TrialRecord{}, err
	}
	dir, err := os.MkdirTemp("", "agenteval-trial-*")
	if err != nil {
		return TrialRecord{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	recordPath, err := filepath.Abs(filepath.Join(options.Out, "records", recordName(scenario.ID, arm.Name, trial)))
	if err != nil {
		return TrialRecord{}, err
	}
	args := []string{
		"worker", "--scenario", path, "--household", options.SimulatorConfig,
		"--trial", strconv.Itoa(trial), "--out", recordPath, "--dir", dir, "--revision", arm.Revision,
		"--provider", arm.Settings.Provider, "--model", arm.Settings.Model, "--reasoning", arm.Settings.ReasoningEffort,
		"--max-requests", strconv.Itoa(maxRequests), "--min-gap", options.MinGap.String(),
	}
	if arm.Settings.BaseURL != "" {
		args = append(args, "--base-url", arm.Settings.BaseURL)
	}
	command := exec.CommandContext(ctx, arm.binary, args...) //nolint:gosec // Runs the worker binary this driver built.
	command.Stderr = options.Log
	if err = command.Run(); err != nil {
		return TrialRecord{}, fmt.Errorf("worker: %w", err)
	}
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		return TrialRecord{}, err
	}
	var record TrialRecord
	if err = json.Unmarshal(raw, &record); err != nil {
		return TrialRecord{}, fmt.Errorf("worker record: %w", err)
	}
	return record, nil
}

func scenarioPath(dir, id string) (string, error) {
	path, err := filepath.Abs(filepath.Join(dir, id+".yaml"))
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(path); err != nil {
		return "", fmt.Errorf("scenario %s must live in %s.yaml: %w", id, id, err)
	}
	return path, nil
}

// LoadRun reads a finished or partial run for reporting.
func LoadRun(out string) (Manifest, []ArmTrial, error) {
	var manifest Manifest
	raw, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		return manifest, nil, err
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return manifest, nil, err
	}
	file, err := os.Open(filepath.Join(out, "trials.jsonl"))
	if err != nil {
		return manifest, nil, err
	}
	defer func() { _ = file.Close() }()
	var trials []ArmTrial
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, maxTrialLineBytes)
	for scanner.Scan() {
		var trial ArmTrial
		if err = json.Unmarshal(scanner.Bytes(), &trial); err != nil {
			return manifest, nil, err
		}
		trials = append(trials, trial)
	}
	return manifest, latestTrials(trials), scanner.Err()
}

// slim drops transcripts from trials.jsonl; records/ keeps the full trial.
func slim(record TrialRecord) TrialRecord {
	record.History = nil
	record.Requests = slices.Clone(record.Requests)
	for index := range record.Requests {
		record.Requests[index].Messages = nil
	}
	return record
}
