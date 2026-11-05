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
		Title:       "Hearth database scale exploration",
		GeneratedAt: time.Now().UTC(),
		Environment: environment(),
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
		return exploreScenarios(ctx, cfg, scenarios, &result, &campaignErr)
	})
	if err != nil {
		campaignErr = errors.Join(campaignErr, err)
		result.Notes = append(result.Notes, err.Error())
	}
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
		seedLimit := (25*time.Minute - seedSpent) / remainingScenarios
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
		result.Manifests = append(result.Manifests, m)
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
			removeRunDatabase(m.Database)
			result.Notes = append(
				result.Notes,
				scenario.Name+": disposable fixture image removed; manifest/hash retained for deterministic regeneration",
			)
		}
	}
	return nil
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
