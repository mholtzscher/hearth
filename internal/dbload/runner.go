package dbload

import (
	"context"
	"database/sql"
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

const (
	blockCopyAllowance  = 2
	warmupIterations    = 3
	warmupSequenceBase  = 1_000_000
	nanosPerMillisecond = 1e6
)

// Run executes a descriptive run or one side of a preregistered pair.
// Every block/case gets a fresh copy of the same closed canonical database.
//
//nolint:funlen // Keep fixture, resource, evidence, and cancellation ordering visible at the entry point.
func Run(ctx context.Context, cfg Config) (ReportData, error) {
	cfg = cfg.Defaults()
	ctx, budgetCancel := context.WithTimeout(ctx, cfg.Budget)
	defer budgetCancel()
	data := ReportData{
		FormatVersion: FormatVersion,
		Title:         "Hearth database scale experiment",
		GeneratedAt:   time.Now().UTC(),
	}
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
	if cfg.ResourceRoot == "" {
		cfg.ResourceRoot = cfg.OutputDir
	}
	if !sourceWithin(cfg.ResourceRoot, cfg.OutputDir) {
		return data, errors.New("run output must be inside its resource root")
	}
	if !sourceWithin(cfg.ResourceRoot, m.Database) {
		info, statErr := os.Stat(m.Database)
		if statErr != nil {
			return data, statErr
		}
		cfg.ReservedBytes = info.Size()
	}
	data.Manifests = []Manifest{m}
	data.Environment = environment()
	ctx, cancel := context.WithTimeout(ctx, cfg.Budget)
	defer cancel()
	ctx, stopMonitoring, err := withResourceMonitor(ctx, cfg)
	if err != nil {
		return data, err
	}
	defer stopMonitoring()
	revision := data.Environment["vcs.revision"]
	if data.Environment["vcs.modified"] == "true" {
		revision = ""
	}
	data.SuiteHash, err = suiteHashAt(cfg.SourceRoot, revision)
	if err != nil {
		return data, err
	}
	coverage, querySQL, err := catalogAt(cfg.SourceRoot, revision)
	if err != nil {
		return data, err
	}
	data.Coverage = coverage
	workloads, err := prepareWorkloads(ctx, m, data.Environment)
	if err != nil {
		return data, err
	}
	selected, err := selectWorkloads(workloads, cfg.Cases)
	if err != nil {
		return data, err
	}
	if cfg.Profile {
		data.Notes = append(data.Notes, "Diagnostic profiler run: excluded from benchmark comparisons.")
		data.Notes = append(
			data.Notes,
			"Diagnostic read captures omit warmup so a query that exceeds the warmup deadline remains profileable; not a comparable timed benchmark.",
		)
	}
	runErr := runWorkloadBlocks(ctx, cfg, m, selected, querySQL, &data)
	padMissingBlocks(cfg, m, selected, &data)
	if cause := context.Cause(ctx); cause != nil {
		data.Notes = append(data.Notes, cause.Error())
		runErr = errors.Join(runErr, cause)
	}
	if err = WriteJSON(filepath.Join(cfg.OutputDir, "results.json"), data); err != nil {
		return data, errors.Join(runErr, err)
	}
	return data, runErr
}

func withResourceMonitor(ctx context.Context, cfg Config) (context.Context, func(), error) {
	if cfg.ResourceRoot == "" {
		return ctx, func() {}, nil
	}
	if err := requireOwnedDirectory(cfg.ResourceRoot); err != nil {
		return ctx, func() {}, err
	}
	ctx, stop := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() { defer close(done); watchResources(ctx, stop, cfg.ResourceRoot, cfg) }()
	return ctx, func() { stop(nil); <-done }, nil
}

func prepareWorkloads(ctx context.Context, m Manifest, env map[string]string) ([]Workload, error) {
	probe, err := platformdb.Open(ctx, m.Database)
	if err != nil {
		return nil, err
	}
	for name, query := range map[string]string{"sqlite.version": "SELECT sqlite_version()", "sqlite.journal_mode": "PRAGMA journal_mode", "sqlite.busy_timeout": "PRAGMA busy_timeout", "sqlite.foreign_keys": "PRAGMA foreign_keys", "sqlite.synchronous": "PRAGMA synchronous", "sqlite.cache_size": "PRAGMA cache_size"} {
		var value string
		if err = probe.QueryRowContext(ctx, query).Scan(&value); err != nil {
			_ = probe.Close()
			return nil, err
		}
		env[name] = value
	}
	workloads, buildErr := Workloads(ctx, probe, m)
	closeErr := probe.Close()
	return workloads, errors.Join(buildErr, closeErr)
}

func runWorkloadBlocks(
	ctx context.Context,
	cfg Config,
	m Manifest,
	selected []Workload,
	querySQL map[string]string,
	data *ReportData,
) error {
	var runErr error
	for block := range cfg.Blocks {
		for _, workload := range selected {
			if err := ctx.Err(); err != nil {
				runErr = err
				break
			}
			summary, blockErr := runBlock(ctx, cfg, m, workload.Name, block, querySQL)
			data.Blocks = append(data.Blocks, summary)
			if summary.Status == statusComplete {
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
	return runErr
}

func padMissingBlocks(cfg Config, m Manifest, selected []Workload, data *ReportData) {
	for _, w := range selected {
		for index := range cfg.Blocks {
			if workloadBlockRecorded(data.Blocks, w.Name, cfg.Pair, index) {
				continue
			}
			data.Blocks = append(
				data.Blocks,
				Block{
					BlockIndex: index, PlannedBlocks: cfg.Blocks, Class: w.Class, RegistrationHash: cfg.Registration,
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
		}
	}
}

func workloadBlockRecorded(blocks []Block, name string, pair, index int) bool {
	for _, block := range blocks {
		if block.Case == name && block.Pair == pair && block.BlockIndex == index {
			return true
		}
	}
	return false
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
			coverage[i].Status = statusTimed
			coverage[i].Reason = ""
			if !slices.Contains(coverage[i].Cases, w.Name) {
				coverage[i].Cases = append(coverage[i].Cases, w.Name)
			}
		}
	}
}

//nolint:funlen // Untimed preparation, timed work, and durable evidence must remain visibly ordered.
func runBlock(
	ctx context.Context,
	cfg Config,
	m Manifest,
	name string,
	index int,
	querySQL map[string]string,
) (Block, error) {
	result := Block{
		BlockIndex: index, PlannedBlocks: cfg.Blocks,
		RegistrationHash: cfg.Registration,
		Case:             name,
		Scenario:         m.Scenario.Name,
		Variant:          cfg.Variant,
		Pair:             cfg.Pair,
		Order:            cfg.Order,
		Concurrency:      cfg.Concurrency,
		Status:           "incomplete",
	}
	database, dbPath, err := openBlockDatabase(ctx, cfg, m, name, index)
	if err != nil {
		return result, err
	}
	defer func() {
		_ = database.Close()
		removeRunDatabase(dbPath)
	}()
	w, err := workloadFor(ctx, database, m, name)
	if err != nil {
		return result, err
	}
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
	if err = warmupWorkload(ctx, cfg, w); err != nil {
		result.Status = statusFailed
		result.Errors = 1
		if errors.Is(err, context.DeadlineExceeded) {
			result.Timeouts = 1
		}
		return result, err
	}
	samples := cfg.Samples
	if w.Once {
		samples = 1
	}
	oracle, err := prepareMutationOracle(ctx, database, m, w, samples)
	if err != nil {
		return result, err
	}
	stopProfile, err := startCPUProfile(cfg, name, index)
	if err != nil {
		return result, err
	}
	defer stopProfile()
	statsBefore := database.Stats()
	stopLoad := captureBlockLoad()
	defer stopLoad()
	blockCtx, cancel := context.WithTimeout(ctx, cfg.CaseLimit)
	defer cancel()
	started := time.Now()
	observations := attempts(blockCtx, cfg, w, samples, index)
	result.WallMS = float64(time.Since(started).Nanoseconds()) / nanosPerMillisecond
	stopProfile()
	load := stopLoad()
	result.LoadStart = load.start
	result.LoadEnd = load.end
	result.LoadPeak = load.peak
	statsAfter := database.Stats()
	result.PoolWaitMS = float64(
		(statsAfter.WaitDuration - statsBefore.WaitDuration).Nanoseconds(),
	) / nanosPerMillisecond
	result = summarize(result, observations, samples)
	if load.err != nil || load.peak > float64(runtime.NumCPU()) {
		result.Status = "invalid-load"
	}
	verificationErr := verifyBlockMutation(ctx, database, oracle, w, observations, &result)
	if err = writeBlockEvidence(cfg, name, index, observations); err != nil {
		result.Status = "incomplete-evidence"
		return result, errors.Join(verificationErr, err)
	}
	if verificationErr != nil {
		return result, verificationErr
	}
	if result.Status != statusComplete {
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

func openBlockDatabase(ctx context.Context, cfg Config, m Manifest, name string, index int) (*sql.DB, string, error) {
	info, err := os.Stat(m.Database)
	if err != nil {
		return nil, "", err
	}
	// Account for source plus copy and a conservative DB-sized WAL/temp allowance.
	resourceRoot := cfg.ResourceRoot
	if resourceRoot == "" {
		resourceRoot = filepath.Dir(m.Database)
	}
	if err = resourceCheck(resourceRoot, cfg, blockCopyAllowance*info.Size()); err != nil {
		return nil, "", err
	}
	dbPath := filepath.Join(cfg.OutputDir, fmt.Sprintf("%s-%d.db", safeCaseName(name), index))
	if err = cloneFile(m.Database, dbPath); err != nil {
		return nil, "", err
	}
	database, err := platformdb.Open(ctx, dbPath)
	if err != nil {
		removeRunDatabase(dbPath)
		return nil, "", err
	}
	if err = platformdb.Migrate(ctx, database); err != nil {
		_ = database.Close()
		removeRunDatabase(dbPath)
		return nil, "", err
	}
	return database, dbPath, nil
}

func workloadFor(ctx context.Context, database *sql.DB, m Manifest, name string) (Workload, error) {
	workloads, err := Workloads(ctx, database, m)
	if err != nil {
		return Workload{}, err
	}
	selected, err := selectWorkloads(workloads, []string{name})
	if err != nil {
		return Workload{}, err
	}
	return selected[0], nil
}

func warmupWorkload(ctx context.Context, cfg Config, w Workload) error {
	if w.Once || (cfg.Profile && !w.Mutates) {
		return nil
	}
	for i := range warmupIterations {
		warmCtx, cancel := context.WithTimeout(ctx, cfg.OperationLimit)
		_, err := w.Run(warmCtx, warmupSequenceBase+i)
		cancel()
		if err != nil {
			return fmt.Errorf("warmup: %w", err)
		}
	}
	return nil
}

func startCPUProfile(cfg Config, name string, index int) (func(), error) {
	if !cfg.Profile {
		return func() {}, nil
	}
	cpu, err := os.Create(
		filepath.Join(cfg.OutputDir, fmt.Sprintf("%s-%d.cpu.pprof", safeCaseName(name), index)),
	)
	if err != nil {
		return nil, err
	}
	if err = pprof.StartCPUProfile(cpu); err != nil {
		_ = cpu.Close()
		return nil, err
	}
	return sync.OnceFunc(func() { pprof.StopCPUProfile(); _ = cpu.Close() }), nil
}

func verifyBlockMutation(
	ctx context.Context,
	database *sql.DB,
	oracle mutationOracle,
	w Workload,
	observations []Observation,
	result *Block,
) error {
	if !w.Mutates || result.Status != statusComplete {
		return nil
	}
	actual, err := verifyMutation(ctx, database, oracle, w)
	if err != nil {
		result.Status = statusFailed
		result.Errors++
		return err
	}
	if w.Once && len(observations) == 1 {
		observations[0].Work = actual
		result.Work = actual
	}
	return nil
}

func writeBlockEvidence(cfg Config, name string, index int, observations []Observation) error {
	path := filepath.Join(
		cfg.OutputDir,
		fmt.Sprintf("%s-%d.observations.jsonl", safeCaseName(name), index),
	)
	if err := writeObservations(path, observations); err != nil {
		return err
	}
	if !cfg.Profile {
		return nil
	}
	return writeHeap(
		filepath.Join(cfg.OutputDir, fmt.Sprintf("%s-%d.heap.pprof", safeCaseName(name), index)),
	)
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
				if err == nil && opCtx.Err() != nil {
					err = opCtx.Err()
				}
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
					observation.TimedOut = errors.Is(err, context.DeadlineExceeded)
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
		latencies = append(latencies, float64(o.ElapsedNS)/nanosPerMillisecond)
	}
	if len(latencies) > 0 {
		block.P50MS = Percentile(latencies, percentileMedian)
		block.P95MS = Percentile(latencies, percentileP95)
		block.P99MS = Percentile(latencies, percentileP99)
	}
	block.Status = statusComplete
	if block.Attempted != target {
		block.Status = "incomplete"
	}
	if block.Errors > 0 {
		block.Status = statusFailed
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
