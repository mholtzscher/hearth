package dbload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// Run executes a descriptive run or one side of a preregistered pair.
// Every block/case gets a fresh copy of the same closed canonical database.
func Run(ctx context.Context, cfg Config) (ReportData, error) {
	cfg = cfg.Defaults()
	data := ReportData{Title: "Hearth database scale experiment", GeneratedAt: time.Now().UTC()}
	if err := cfg.Validate(); err != nil {
		return data, err
	}
	m, err := ReadManifest(cfg.Fixture)
	if err != nil {
		return data, err
	}
	if err = createOwnedDirectory(cfg.OutputDir); err != nil {
		return data, err
	}
	data.Manifests = []Manifest{m}
	data.Environment = environment()
	ctx, cancel := context.WithTimeout(ctx, cfg.Budget)
	defer cancel()
	if cfg.ResourceRoot != "" {
		if err = requireOwnedDirectory(cfg.ResourceRoot); err != nil {
			return data, err
		}
		var stop context.CancelCauseFunc
		ctx, stop = context.WithCancelCause(ctx)
		done := make(chan struct{})
		go func() { defer close(done); watchResources(ctx, stop, cfg.ResourceRoot, cfg) }()
		defer func() { stop(nil); <-done }()
	}
	revision := data.Environment["vcs.revision"]
	if data.Environment["vcs.modified"] == "true" {
		revision = ""
	}
	coverage, querySQL, err := catalogAt(cfg.SourceRoot, revision)
	if err != nil {
		return data, err
	}
	data.Coverage = coverage
	probe, err := platformdb.Open(ctx, m.Database)
	if err != nil {
		return data, err
	}
	workloads, buildErr := Workloads(ctx, probe, m)
	closeErr := probe.Close()
	if err = errors.Join(buildErr, closeErr); err != nil {
		return data, err
	}
	selected, err := selectWorkloads(workloads, cfg.Cases)
	if err != nil {
		return data, err
	}
	if cfg.Profile {
		data.Notes = append(data.Notes, "Diagnostic profiler run: excluded from benchmark comparisons.")
	}
	var runErr error
	for _, workload := range selected {
		for block := range cfg.Blocks {
			if err = ctx.Err(); err != nil {
				runErr = err
				break
			}
			summary, blockErr := runBlock(ctx, cfg, m, workload.Name, block, querySQL)
			data.Blocks = append(data.Blocks, summary)
			if summary.Status == "complete" {
				markCoverage(data.Coverage, workload)
			}
			if blockErr != nil {
				data.Notes = append(data.Notes, fmt.Sprintf("%s block%d: %v", workload.Name, block, blockErr))
				runErr = errors.Join(runErr, blockErr)
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	for _, w := range selected {
		for index := range cfg.Blocks {
			found := false
			for _, block := range data.Blocks {
				if block.Case == w.Name && block.Pair == cfg.Pair {
					found = true
					break
				}
			}
			if !found {
				data.Blocks = append(
					data.Blocks,
					Block{
						Case:        w.Name,
						Scenario:    m.Scenario.Name,
						Variant:     cfg.Variant,
						Pair:        cfg.Pair,
						Order:       cfg.Order,
						Concurrency: cfg.Concurrency,
						Status:      "resource-limited",
					},
				)
				data.Notes = append(data.Notes, fmt.Sprintf("%s block%d not attempted within budget", w.Name, index))
				break
			}
		}
	}
	if cause := context.Cause(ctx); cause != nil {
		data.Notes = append(data.Notes, cause.Error())
		runErr = errors.Join(runErr, cause)
	}
	if err = WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), data); err != nil {
		return data, errors.Join(runErr, err)
	}
	return data, runErr
}

func selectWorkloads(all []Workload, names []string) ([]Workload, error) {
	if len(names) == 0 {
		return all, nil
	}
	var selected []Workload
	for _, name := range names {
		found := false
		for _, w := range all {
			if w.Name == name {
				selected = append(selected, w)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown workload %q", name)
		}
	}
	return selected, nil
}

func markCoverage(coverage []QueryCoverage, w Workload) {
	for i := range coverage {
		if slices.Contains(w.Queries, coverage[i].Owner+"/"+coverage[i].Name) {
			coverage[i].Status = "timed"
			coverage[i].Reason = ""
			if !slices.Contains(coverage[i].Cases, w.Name) {
				coverage[i].Cases = append(coverage[i].Cases, w.Name)
			}
		}
	}
}

func runBlock(
	ctx context.Context,
	cfg Config,
	m Manifest,
	name string,
	index int,
	querySQL map[string]string,
) (Block, error) {
	result := Block{
		RegistrationHash: cfg.Registration,
		Case:             name,
		Scenario:         m.Scenario.Name,
		Variant:          cfg.Variant,
		Pair:             cfg.Pair,
		Order:            cfg.Order,
		Concurrency:      cfg.Concurrency,
		Status:           "incomplete",
	}
	info, err := os.Stat(m.Database)
	if err != nil {
		return result, err
	}
	// Account for source plus copy and a conservative DB-sized WAL/temp allowance.
	resourceRoot := cfg.ResourceRoot
	if resourceRoot == "" {
		resourceRoot = filepath.Dir(m.Database)
	}
	if err = resourceCheck(resourceRoot, cfg, 2*info.Size()); err != nil {
		return result, err
	}
	dbPath := filepath.Join(cfg.OutputDir, fmt.Sprintf("%s-%d.db", safeCaseName(name), index))
	if err = cloneFile(m.Database, dbPath); err != nil {
		return result, err
	}
	defer removeRunDatabase(dbPath)
	database, err := platformdb.Open(ctx, dbPath)
	if err != nil {
		return result, err
	}
	defer database.Close()
	if err = platformdb.Migrate(ctx, database); err != nil {
		return result, err
	}
	workloads, err := Workloads(ctx, database, m)
	if err != nil {
		return result, err
	}
	selected, err := selectWorkloads(workloads, []string{name})
	if err != nil {
		return result, err
	}
	w := selected[0]
	result.Class = w.Class
	if cfg.Profile {
		if err = capturePlans(
			ctx,
			database,
			querySQL,
			w,
			filepath.Join(cfg.OutputDir, safeCaseName(name)+"-plans.json"),
		); err != nil {
			return result, err
		}
	}
	// Warmup is equal, outside the timer, and omitted for destructive one-shot work.
	if !w.Once {
		for i := range 3 {
			if _, err = w.Run(ctx, 1000000+i); err != nil {
				return result, fmt.Errorf("warmup: %w", err)
			}
		}
	}
	samples := cfg.Samples
	if w.Once {
		samples = 1
	}
	oracle, err := prepareMutationOracle(ctx, database, m, w, samples)
	if err != nil {
		return result, err
	}
	var cpu *os.File
	if cfg.Profile {
		cpu, err = os.Create(filepath.Join(cfg.OutputDir, fmt.Sprintf("%s-%d.cpu.pprof", safeCaseName(name), index)))
		if err != nil {
			return result, err
		}
		if err = pprof.StartCPUProfile(cpu); err != nil {
			_ = cpu.Close()
			return result, err
		}
		defer func() { pprof.StopCPUProfile(); _ = cpu.Close() }()
	}
	statsBefore := database.Stats()
	blockCtx, cancel := context.WithTimeout(ctx, cfg.CaseLimit)
	defer cancel()
	started := time.Now()
	observations := attempts(blockCtx, cfg, w, samples, index)
	result.WallMS = float64(time.Since(started).Nanoseconds()) / 1e6
	statsAfter := database.Stats()
	result.PoolWaitMS = float64((statsAfter.WaitDuration - statsBefore.WaitDuration).Nanoseconds()) / 1e6
	result = summarize(result, observations, samples)
	if w.Mutates && result.Status == "complete" {
		actual, verifyErr := verifyMutation(ctx, database, oracle, w)
		if verifyErr != nil {
			result.Status = "failed"
			result.Errors++
			return result, verifyErr
		}
		if w.Once && len(observations) == 1 {
			observations[0].Work = actual
			result.Work = actual
		}
	}
	path := filepath.Join(cfg.OutputDir, fmt.Sprintf("%s-%d.observations.jsonl", safeCaseName(name), index))
	if err = writeObservations(path, observations); err != nil {
		return result, err
	}
	if cfg.Profile {
		if err = writeHeap(
			filepath.Join(cfg.OutputDir, fmt.Sprintf("%s-%d.heap.pprof", safeCaseName(name), index)),
		); err != nil {
			return result, err
		}
	}
	if result.Status != "complete" {
		return result, fmt.Errorf(
			"block %s: %d/%d completed; %d errors, %d timeouts",
			result.Status,
			result.Completed,
			samples,
			result.Errors,
			result.Timeouts,
		)
	}
	return result, nil
}

func attempts(ctx context.Context, cfg Config, w Workload, samples, index int) []Observation {
	var next atomic.Int64
	results := make(chan Observation, samples)
	var workers sync.WaitGroup
	for range cfg.Concurrency {
		workers.Go(func() {
			for {
				sequence := int(next.Add(1) - 1)
				if sequence >= samples || ctx.Err() != nil {
					return
				}
				opCtx, cancel := context.WithTimeout(ctx, cfg.OperationLimit)
				start := time.Now()
				work, err := w.Run(opCtx, sequence)
				elapsed := time.Since(start)
				cancel()
				observation := Observation{
					Case:         w.Name,
					ParameterSet: w.Name,
					Concurrency:  cfg.Concurrency,
					Pair:         cfg.Pair,
					PairOrder:    cfg.Order,
					Block:        index,
					Variant:      cfg.Variant,
					Sequence:     sequence,
					ElapsedNS:    elapsed.Nanoseconds(),
					Work:         work,
					Correct:      err == nil,
				}
				if err != nil {
					observation.ErrorClass = err.Error()
					observation.TimedOut = errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
				}
				results <- observation
			}
		})
	}
	workers.Wait()
	close(results)
	observations := make([]Observation, 0, samples)
	for observation := range results {
		observations = append(observations, observation)
	}
	slices.SortFunc(observations, func(a, b Observation) int { return a.Sequence - b.Sequence })
	return observations
}

func summarize(block Block, observations []Observation, target int) Block {
	latencies := make([]float64, 0, len(observations))
	for _, o := range observations {
		block.Attempted++
		if !o.Correct || o.ErrorClass != "" {
			block.Errors++
			if o.TimedOut {
				block.Timeouts++
			}
			continue
		}
		block.Completed++
		block.Work += o.Work
		latencies = append(latencies, float64(o.ElapsedNS)/1e6)
	}
	if len(latencies) > 0 {
		block.P50MS = Percentile(latencies, 0.50)
		block.P95MS = Percentile(latencies, 0.95)
		block.P99MS = Percentile(latencies, 0.99)
	}
	block.Status = "complete"
	if block.Attempted != target {
		block.Status = "incomplete"
	}
	if block.Errors > 0 {
		block.Status = "failed"
	}
	return block
}

func writeObservations(path string, observations []Observation) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(f)
	for _, o := range observations {
		if err = encoder.Encode(o); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	return f.Close()
}

func removeRunDatabase(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}

func writeHeap(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	runtime.GC()
	return errors.Join(pprof.WriteHeapProfile(f), f.Close())
}
