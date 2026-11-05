package dbload

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

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
	if slot < 1 || slot > 3 || len(cfg.Cases) == 0 {
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
	beforeHash, err := fileHash(baseline)
	if err != nil {
		return data, err
	}
	afterHash, err := fileHash(candidate)
	if err != nil {
		return data, err
	}
	suiteHash, err := suiteHash(cfg.SourceRoot)
	if err != nil {
		return data, err
	}
	schedule, _ := json.Marshal(struct {
		Cases       []string
		Samples     int
		Concurrency int
		Fixture     string
	}{cfg.Cases, cfg.Samples, cfg.Concurrency, m.FixtureHash})
	reg := Registration{
		FormatVersion:  FormatVersion,
		Slot:           slot,
		BaselineHash:   beforeHash,
		CandidateHash:  afterHash,
		SuiteHash:      suiteHash,
		FixtureHash:    m.FixtureHash,
		ScheduleHash:   hashBytes(schedule),
		Cells:          cfg.Cases,
		Pairs:          ConfirmationPairs,
		SamplesPerCell: ConfirmationSamples,
		MinimumGain:    0.10,
		Alpha:          0.05 / 3,
	}
	raw, err := json.Marshal(reg)
	if err != nil {
		return data, err
	}
	reg.ContentHash = hashBytes(raw)
	seedBytes, err := hex.DecodeString(reg.ContentHash[:16])
	if err != nil {
		return data, err
	}
	reg.ResamplingSeed = int64(binary.BigEndian.Uint64(seedBytes) & 0x7fffffffffffffff)
	if err = WriteJSON(filepath.Join(cfg.OutputDir, "registration.json"), reg); err != nil {
		return data, err
	}
	data.Registrations = []Registration{reg}
	data.Manifests = []Manifest{m}
	ctx, cancel := context.WithTimeout(ctx, cfg.Budget)
	defer cancel()
	var campaignErr error
	for pairIndex := range ConfirmationPairs {
		pair := pairIndex + 1
		order := "AB"
		binaries := []string{baseline, candidate}
		variants := []string{"baseline", "candidate"}
		if pairIndex%2 == 1 {
			order = "BA"
			binaries = []string{candidate, baseline}
			variants = []string{"candidate", "baseline"}
		}
		load, loadErr := HostLoad()
		if loadErr != nil || load > float64(runtime.NumCPU()) {
			campaignErr = fmt.Errorf(
				"host load invalidates planned pair%d (load=%g, error=%v); confirmation incomplete",
				pair,
				load,
				loadErr,
			)
			break
		}
		for side, binaryPath := range binaries {
			path := filepath.Join(cfg.OutputDir, fmt.Sprintf("pair-%02d-%s", pair, variants[side]))
			args := []string{
				"run",
				"-paired-worker",
				"-registration", reg.ContentHash,
				"-fixture",
				cfg.Fixture,
				"-out",
				path,
				"-source-root",
				cfg.SourceRoot,
				"-cases",
				strings.Join(cfg.Cases, ","),
				"-samples",
				strconv.Itoa(cfg.Samples),
				"-blocks",
				"1",
				"-concurrency",
				strconv.Itoa(cfg.Concurrency),
				"-variant",
				variants[side],
				"-pair",
				strconv.Itoa(pair),
				"-order",
				order,
				"-budget",
				cfg.Budget.String(),
				"-case-limit",
				cfg.CaseLimit.String(),
			}
			if cfg.ResourceRoot != "" {
				args = append(args, "-resource-root", cfg.ResourceRoot)
			}
			command := exec.CommandContext(ctx, binaryPath, args...)
			command.Env = append(os.Environ(), "GOMEMLIMIT=4GiB")
			output, runErr := command.CombinedOutput()
			if runErr != nil {
				campaignErr = errors.Join(
					campaignErr,
					fmt.Errorf("pair%d %s: %w: %s", pair, variants[side], runErr, output),
				)
			}
			child, readErr := readReportData(filepath.Join(path, "results.json"))
			if readErr != nil {
				campaignErr = errors.Join(campaignErr, readErr)
			} else {
				data.Blocks = append(data.Blocks, child.Blocks...)
				data.Artifacts = append(data.Artifacts, path)
				data.Notes = append(data.Notes, child.Notes...)
			}
			if ctx.Err() != nil {
				break
			}
		}
		// Persist raw evidence after every pair, without performing interim inference.
		if err = WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), data); err != nil {
			return data, errors.Join(campaignErr, err)
		}
		if ctx.Err() != nil {
			campaignErr = errors.Join(campaignErr, ctx.Err())
			break
		}
	}
	data.Comparisons, err = Analyze(reg, data.Blocks)
	if err != nil {
		campaignErr = errors.Join(campaignErr, err)
	}
	for _, name := range reg.Cells {
		for _, variant := range []string{"baseline", "candidate"} {
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
	if campaignErr != nil {
		data.Notes = append(data.Notes, "Incomplete/failing confirmation: "+campaignErr.Error())
	}
	if err = WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), data); err != nil {
		return data, errors.Join(campaignErr, err)
	}
	return data, campaignErr
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
		for key, value := range other.Environment {
			data.Environment[key] = value
		}
	}
	return RenderReport(output, data)
}

func mergeCoverage(existing, incoming []QueryCoverage) []QueryCoverage {
	for _, item := range incoming {
		found := false
		for i := range existing {
			if existing[i].Owner == item.Owner && existing[i].Name == item.Name {
				found = true
				for _, name := range item.Cases {
					if !contains(existing[i].Cases, name) {
						existing[i].Cases = append(existing[i].Cases, name)
					}
				}
				if len(existing[i].Cases) > 0 {
					existing[i].Status = "timed"
					existing[i].Reason = ""
				}
				break
			}
		}
		if !found {
			existing = append(existing, item)
		}
	}
	return existing
}

func contains(items []string, item string) bool {
	for _, value := range items {
		if value == item {
			return true
		}
	}
	return false
}
