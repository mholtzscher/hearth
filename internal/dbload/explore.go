package dbload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultScenarios reserves early coverage for each largest independent dimension.
// The shared 1x fixture is not repeatedly measured under different axis labels.
func DefaultScenarios() []Scenario {
	result := []Scenario{
		{Name: "population-1", Dimension: dimensionPopulation, Scale: 1, Concurrency: 1, Seed: 1},
	}
	for _, scale := range []int{1000, 10, 100} {
		for _, dimension := range []string{dimensionPopulation, dimensionHistory, dimensionFanout} {
			if dimension == dimensionFanout {
				for _, relation := range []string{relationDevices, relationMessages, relationAutomations} {
					result = append(
						result,
						Scenario{
							Name:        fmt.Sprintf("fanout-%s-%d", relation, scale),
							Dimension:   dimension,
							Scale:       scale,
							Relation:    relation,
							Concurrency: 1,
							Seed:        1,
						},
					)
				}
			} else {
				result = append(
					result,
					Scenario{
						Name:        fmt.Sprintf("%s-%d", dimension, scale),
						Dimension:   dimension,
						Scale:       scale,
						Concurrency: 1,
						Seed:        1,
					},
				)
			}
		}
	}
	return append(
		result,
		Scenario{
			Name:        "combined-10x10x10",
			Dimension:   dimensionCombined,
			Scale:       combinedPopulationScale,
			Concurrency: 1,
			Seed:        1,
		},
	)
}

// Explore runs a budgeted descriptive matrix. Fix selection remains evidence-led.
func Explore(ctx context.Context, cfg Config, scenarios []Scenario) (ReportData, error) {
	cfg = cfg.Defaults()
	result := ReportData{
		FormatVersion: FormatVersion,
		Title:         "Hearth database scale exploration",
		GeneratedAt:   time.Now().UTC(),
		Environment:   environment(),
	}
	var hashErr error
	result.SuiteHash, hashErr = suiteHash(cfg.SourceRoot)
	if hashErr != nil {
		return result, hashErr
	}
	if err := cfg.Validate(); err != nil {
		return result, err
	}
	if len(scenarios) == 0 {
		scenarios = DefaultScenarios()
	}
	for _, scenario := range scenarios {
		if err := ValidateScenario(scenario); err != nil {
			return result, err
		}
	}
	if err := createOwnedDirectory(cfg.OutputDir); err != nil {
		return result, err
	}
	cfg.ResourceRoot = cfg.OutputDir
	result.Notes = []string{
		"Exploratory results only: three short blocks, not confirmatory significance tests. Missing cases are not passes.",
		"1x is the shared population-1 fixture for every independent axis. Combined = population10 x devices/Adapter10 x retained-history10.",
		"Maximum initial execution allocation: seed/pilots25m, exploration35m; profiles10m and confirmation40m reserved separately. All execution is charged to budget.json.",
	}
	if err := WriteJSON(filepath.Join(cfg.OutputDir, "schedule.json"), scenarios); err != nil {
		return result, err
	}
	var campaignErr error
	err := WithBudget(ctx, cfg.OutputDir, cfg.Budget, func(ctx context.Context) error {
		ctx, stop, monitorErr := withResourceMonitor(ctx, cfg)
		if monitorErr != nil {
			return monitorErr
		}
		defer stop()
		return exploreScenarios(ctx, cfg, scenarios, &result, &campaignErr)
	})
	if err != nil {
		campaignErr = errors.Join(campaignErr, err)
		result.Notes = append(result.Notes, err.Error())
	}
	appendSharedBaselines(&result)
	writeErr := WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), result)
	return result, errors.Join(campaignErr, writeErr)
}

func exploreScenarios(
	ctx context.Context,
	cfg Config,
	scenarios []Scenario,
	result *ReportData,
	campaignErr *error,
) error {
	var seedSpent, runSpent time.Duration
	for index, scenario := range scenarios {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		remainingScenarios := time.Duration(len(scenarios) - index)
		seedLimit := seedTimeAllocation(scenarios[index:], 25*time.Minute-seedSpent)
		if seedLimit <= 0 {
			result.Notes = append(result.Notes, scenario.Name+": seed allocation exhausted; not run")
			continue
		}
		seedDir := filepath.Join(cfg.OutputDir, "fixtures", scenario.Name)
		start := time.Now()
		seedCtx, seedCancel := context.WithTimeout(ctx, seedLimit)
		m, seedErr := Seed(seedCtx, seedDir, scenario)
		seedCancel()
		seedSpent += time.Since(start)
		if seedErr != nil {
			*campaignErr = errors.Join(*campaignErr, seedErr)
			result.Notes = append(result.Notes, scenario.Name+": seed incomplete: "+seedErr.Error())
			_ = WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), *result)
			continue
		}
		manifestPath := filepath.Join(seedDir, "manifest.json")
		if writeErr := WriteJSON(manifestPath, m); writeErr != nil {
			return writeErr
		}
		recorded := m
		recorded.Database = filepath.Join(seedDir, m.Database)
		result.Manifests = append(result.Manifests, recorded)
		if err := exploreConcurrencies(
			ctx,
			cfg,
			scenario,
			manifestPath,
			remainingScenarios,
			&runSpent,
			result,
		); err != nil {
			return err
		}
		// Retain small 1x and selected diagnostic fixtures; free other disposable images.
		keep := scenario.Scale == 1 || scenario.Name == "history-1000" || scenario.Name == "fanout-messages-1000"
		if !keep {
			removeRunDatabase(filepath.Join(seedDir, m.Database))
			result.Notes = append(
				result.Notes,
				scenario.Name+": disposable fixture image removed; manifest/hash retained for deterministic regeneration",
			)
		}
	}
	return nil
}

func appendSharedBaselines(result *ReportData) {
	var base Manifest
	found := false
	for _, m := range result.Manifests {
		if m.Scenario.Name == "population-1" {
			base = m
			found = true
			break
		}
	}
	if !found {
		return
	}
	for _, axis := range []struct{ dimension, relation string }{{dimensionHistory, ""}, {dimensionFanout, relationDevices}, {dimensionFanout, relationMessages}, {dimensionFanout, relationAutomations}} {
		m := base
		m.AliasOf = base.Scenario.Name
		m.Scenario.Dimension = axis.dimension
		m.Scenario.Relation = axis.relation
		m.Scenario.Name = axis.dimension + "-1"
		if axis.relation != "" {
			m.Scenario.Name = axis.dimension + "-" + axis.relation + "-1"
		}
		m.Limits = append(
			append([]string{}, m.Limits...),
			"shared baseline observations from "+m.AliasOf+"; not independent replications",
		)
		result.Manifests = append(result.Manifests, m)
	}
	result.Notes = append(
		result.Notes,
		"history-1 and fanout-*-1 are explicit aliases of population-1. They reuse the same observations and do not add experimental replications.",
	)
}

func seedTimeAllocation(scenarios []Scenario, remaining time.Duration) time.Duration {
	if remaining <= 0 || len(scenarios) == 0 {
		return 0
	}
	weights := make([]int64, len(scenarios))
	var total int64
	for i, scenario := range scenarios {
		plan, err := planFor(scenario)
		if err != nil {
			return 0
		}
		weights[i] = int64(plan.retainedHistoryRows() + plan.messageCount() + plan.entities)
		total += weights[i]
	}
	const minimumSeedWindow = 30 * time.Second
	return min(remaining, max(minimumSeedWindow, time.Duration(float64(remaining)*float64(weights[0])/float64(total))))
}

func exploreConcurrencies(
	ctx context.Context,
	cfg Config,
	scenario Scenario,
	manifestPath string,
	remainingScenarios time.Duration,
	runSpent *time.Duration,
	result *ReportData,
) error {
	for _, concurrency := range []int{1, 4, 16} {
		remaining := (35*time.Minute - *runSpent) / remainingScenarios
		fraction := 0.15
		if concurrency == 1 {
			fraction = 0.70
		}
		runLimit := time.Duration(float64(remaining) * fraction)
		if runLimit < time.Second {
			result.Notes = append(result.Notes, scenario.Name+": exploration allocation exhausted")
			break
		}
		child := cfg
		child.OutputDir = filepath.Join(
			cfg.OutputDir,
			"runs",
			fmt.Sprintf("%s-c%d", scenario.Name, concurrency),
		)
		child.Fixture = manifestPath
		child.Concurrency = concurrency
		child.Budget = runLimit
		start := time.Now()
		data, runErr := Run(ctx, child)
		*runSpent += time.Since(start)
		result.Blocks = append(result.Blocks, data.Blocks...)
		result.Coverage = mergeCoverage(result.Coverage, data.Coverage)
		result.Notes = append(result.Notes, data.Notes...)
		result.Artifacts = append(result.Artifacts, filepath.Join(child.OutputDir, "results.json"))
		if runErr != nil {
			result.Notes = append(result.Notes, scenario.Name+": "+runErr.Error())
		}
		if writeErr := WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), *result); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

// ResultFiles lists top-level suite evidence selected explicitly by the caller.
func ResultFiles(paths []string) ([]string, error) {
	var result []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			path = filepath.Join(path, "results.json")
		}
		result = append(result, path)
	}
	return result, nil
}
