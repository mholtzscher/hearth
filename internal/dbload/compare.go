package dbload

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxResamplingSeed = 1<<63 - 1

type confirmationPairPlan struct {
	order    string
	binaries []string
	variants []string
}

type confirmationOutcome struct {
	campaignErr error
	persistErr  error
}

// Compare registers a fixed family, then reruns both executables in every pair.
// It never reads interim p-values or treats a partial confirmation as a win.
func Compare(ctx context.Context, cfg Config, baseline, candidate string, slot int) (ReportData, error) {
	cfg = cfg.Defaults()
	cfg.Blocks = 1
	cfg.Samples = ConfirmationSamples
	data := ReportData{
		Title:       "Hearth database before / after",
		GeneratedAt: time.Now().UTC(),
		Environment: environment(),
	}
	if err := cfg.Validate(); err != nil {
		return data, err
	}
	if slot < 1 || slot > confirmationSlots || len(cfg.Cases) == 0 {
		return data, errors.New("confirmation requires slot1..3 and explicit registered cases")
	}
	m, err := ReadManifest(cfg.Fixture)
	if err != nil {
		return data, err
	}
	if err = createOwnedDirectory(cfg.OutputDir); err != nil {
		return data, err
	}
	baseline, err = filepath.Abs(baseline)
	if err != nil {
		return data, err
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return data, err
	}
	reg, err := registerConfirmation(cfg, m, baseline, candidate, slot)
	if err != nil {
		return data, err
	}
	data.Registrations = []Registration{reg}
	data.Manifests = []Manifest{m}
	outcome := runConfirmation(ctx, cfg, reg, baseline, candidate, &data)
	if outcome.persistErr != nil {
		return data, errors.Join(outcome.campaignErr, outcome.persistErr)
	}
	campaignErr := outcome.campaignErr
	data.Comparisons, err = Analyze(reg, data.Blocks)
	if err != nil {
		campaignErr = errors.Join(campaignErr, err)
	}
	appendGateNotes(&data, reg)
	if campaignErr != nil {
		data.Notes = append(data.Notes, "Incomplete/failing confirmation: "+campaignErr.Error())
	}
	if err = WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), data); err != nil {
		return data, errors.Join(campaignErr, err)
	}
	return data, campaignErr
}

func registerConfirmation(cfg Config, m Manifest, baseline, candidate string, slot int) (Registration, error) {
	beforeHash, err := fileHash(baseline)
	if err != nil {
		return Registration{}, err
	}
	afterHash, err := fileHash(candidate)
	if err != nil {
		return Registration{}, err
	}
	suiteHashValue, err := suiteHash(cfg.SourceRoot)
	if err != nil {
		return Registration{}, err
	}
	schedule, err := json.Marshal(struct {
		Cases       []string `json:"Cases"`
		Samples     int      `json:"Samples"`
		Concurrency int      `json:"Concurrency"`
		Fixture     string   `json:"Fixture"`
	}{cfg.Cases, cfg.Samples, cfg.Concurrency, m.FixtureHash})
	if err != nil {
		return Registration{}, err
	}
	reg := Registration{
		FormatVersion:  FormatVersion,
		Slot:           slot,
		BaselineHash:   beforeHash,
		CandidateHash:  afterHash,
		SuiteHash:      suiteHashValue,
		FixtureHash:    m.FixtureHash,
		ScheduleHash:   hashBytes(schedule),
		Cells:          cfg.Cases,
		Pairs:          ConfirmationPairs,
		SamplesPerCell: ConfirmationSamples,
		MinimumGain:    minimumGain,
		Alpha:          familyAlpha / confirmationSlots,
	}
	raw, err := json.Marshal(reg)
	if err != nil {
		return Registration{}, err
	}
	reg.ContentHash = hashBytes(raw)
	seedBytes, err := hex.DecodeString(reg.ContentHash[:16])
	if err != nil {
		return Registration{}, err
	}
	reg.ResamplingSeed = int64(binary.BigEndian.Uint64(seedBytes) & maxResamplingSeed)
	if err = WriteJSON(filepath.Join(cfg.OutputDir, "registration.json"), reg); err != nil {
		return Registration{}, err
	}
	return reg, nil
}

func runConfirmation(
	ctx context.Context,
	cfg Config,
	reg Registration,
	baseline, candidate string,
	data *ReportData,
) confirmationOutcome {
	ctx, cancel := context.WithTimeout(ctx, cfg.Budget)
	defer cancel()
	var outcome confirmationOutcome
	for pairIndex := range ConfirmationPairs {
		pair := pairIndex + 1
		plan := confirmationPairFor(pairIndex, baseline, candidate)
		if err := checkHostLoad(pair); err != nil {
			outcome.campaignErr = errors.Join(outcome.campaignErr, err)
			return outcome
		}
		for side := range plan.binaries {
			sideErr := runPairSide(ctx, cfg, reg, data, pair, plan, side)
			if sideErr != nil {
				outcome.campaignErr = errors.Join(outcome.campaignErr, sideErr)
			}
			if ctx.Err() != nil {
				break
			}
		}
		// Persist raw evidence after every pair, without performing interim inference.
		if err := WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), data); err != nil {
			outcome.persistErr = err
			return outcome
		}
		if ctx.Err() != nil {
			outcome.campaignErr = errors.Join(outcome.campaignErr, ctx.Err())
			return outcome
		}
	}
	return outcome
}

func confirmationPairFor(pairIndex int, baseline, candidate string) confirmationPairPlan {
	plan := confirmationPairPlan{
		order:    "AB",
		binaries: []string{baseline, candidate},
		variants: []string{variantBaseline, variantCandidate},
	}
	if pairIndex%2 == 1 {
		plan.order = "BA"
		plan.binaries = []string{candidate, baseline}
		plan.variants = []string{variantCandidate, variantBaseline}
	}
	return plan
}

func checkHostLoad(pair int) error {
	load, err := HostLoad()
	if err == nil && load <= float64(runtime.NumCPU()) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"host load invalidates planned pair%d (load=%g): %w; confirmation incomplete",
			pair,
			load,
			err,
		)
	}
	return fmt.Errorf(
		"host load invalidates planned pair%d (load=%g); confirmation incomplete",
		pair,
		load,
	)
}

func runPairSide(
	ctx context.Context,
	cfg Config,
	reg Registration,
	data *ReportData,
	pair int,
	plan confirmationPairPlan,
	side int,
) error {
	path := filepath.Join(cfg.OutputDir, fmt.Sprintf("pair-%02d-%s", pair, plan.variants[side]))
	args := confirmationArgs(cfg, reg, plan, side, pair, path)
	//nolint:gosec // binaries are caller-registered frozen executables, not untrusted input.
	command := exec.CommandContext(ctx, plan.binaries[side], args...)
	command.Env = append(os.Environ(), "GOMEMLIMIT=4GiB")
	output, runErr := command.CombinedOutput()
	var sideErr error
	if runErr != nil {
		sideErr = fmt.Errorf("pair%d %s: %w: %s", pair, plan.variants[side], runErr, output)
	}
	child, readErr := readReportData(filepath.Join(path, "results.json"))
	if readErr != nil {
		return errors.Join(sideErr, readErr)
	}
	data.Blocks = append(data.Blocks, child.Blocks...)
	data.Artifacts = append(data.Artifacts, path)
	data.Notes = append(data.Notes, child.Notes...)
	return sideErr
}

func confirmationArgs(
	cfg Config,
	reg Registration,
	plan confirmationPairPlan,
	side, pair int,
	path string,
) []string {
	args := []string{
		"run",
		"-paired-worker",
		"-registration", reg.ContentHash,
		"-fixture", cfg.Fixture,
		"-out", path,
		"-source-root", cfg.SourceRoot,
		"-cases", strings.Join(cfg.Cases, ","),
		"-samples", strconv.Itoa(cfg.Samples),
		"-blocks", "1",
		"-concurrency", strconv.Itoa(cfg.Concurrency),
		"-variant", plan.variants[side],
		"-pair", strconv.Itoa(pair),
		"-order", plan.order,
		"-budget", cfg.Budget.String(),
		"-case-limit", cfg.CaseLimit.String(),
	}
	if cfg.ResourceRoot != "" {
		args = append(args, "-resource-root", cfg.ResourceRoot)
	}
	return args
}

func appendGateNotes(data *ReportData, reg Registration) {
	for _, name := range reg.Cells {
		for _, variant := range []string{variantBaseline, variantCandidate} {
			var cell []Block
			for _, block := range data.Blocks {
				if block.Case == name && block.Variant == variant {
					cell = append(cell, block)
				}
			}
			g := EvaluateGate(cell, reg.ResamplingSeed)
			data.Notes = append(
				data.Notes,
				fmt.Sprintf("Absolute gate %s %s: %s; %s", name, variant, g.Status, g.Reason),
			)
		}
	}
}

func suiteHash(root string) (string, error) {
	files, err := filepath.Glob(filepath.Join(root, "internal/dbload/*"))
	if err != nil {
		return "", err
	}
	var combined []byte
	for _, path := range files {
		if filepath.Ext(path) != ".go" && filepath.Ext(path) != ".html" {
			continue
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", readErr
		}
		combined = append(combined, filepath.Base(path)...)
		combined = append(combined, contents...)
	}
	entry, err := os.ReadFile(filepath.Join(root, "internal/cmd/dbload/main.go"))
	if err != nil {
		return "", err
	}
	combined = append(combined, entry...)
	return hashBytes(combined), nil
}

func readReportData(path string) (ReportData, error) {
	var data ReportData
	raw, err := os.ReadFile(path)
	if err != nil {
		return data, err
	}
	err = json.Unmarshal(raw, &data)
	return data, err
}

// Report merges retained evidence without executing any workload.
func Report(paths []string, output string) error {
	data := ReportData{Title: "Hearth database scale study", GeneratedAt: time.Now().UTC(), Environment: environment()}
	for _, path := range paths {
		other, err := readReportData(path)
		if err != nil {
			return err
		}
		data.Manifests = append(data.Manifests, other.Manifests...)
		data.Blocks = append(data.Blocks, other.Blocks...)
		data.Comparisons = append(data.Comparisons, other.Comparisons...)
		data.Registrations = append(data.Registrations, other.Registrations...)
		data.Notes = append(data.Notes, other.Notes...)
		data.Artifacts = append(data.Artifacts, path)
		data.Artifacts = append(data.Artifacts, other.Artifacts...)
		data.Coverage = mergeCoverage(data.Coverage, other.Coverage)
		maps.Copy(data.Environment, other.Environment)
	}
	return RenderReport(output, data)
}

func mergeCoverage(existing, incoming []QueryCoverage) []QueryCoverage {
	for _, item := range incoming {
		index := coverageIndex(existing, item)
		if index < 0 {
			existing = append(existing, item)
			continue
		}
		existing[index] = mergeCoverageItem(existing[index], item)
	}
	return existing
}

func coverageIndex(coverage []QueryCoverage, item QueryCoverage) int {
	for index := range coverage {
		if coverage[index].Owner == item.Owner && coverage[index].Name == item.Name {
			return index
		}
	}
	return -1
}

func mergeCoverageItem(existing, incoming QueryCoverage) QueryCoverage {
	for _, name := range incoming.Cases {
		if !slices.Contains(existing.Cases, name) {
			existing.Cases = append(existing.Cases, name)
		}
	}
	if len(existing.Cases) > 0 {
		existing.Status = statusTimed
		existing.Reason = ""
	}
	return existing
}
