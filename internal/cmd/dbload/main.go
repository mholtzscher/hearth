// dbload is the isolated developer database scale-test command.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/mholtzscher/hearth/internal/dbload"
)

const debugMemoryLimit = 4 << 30

type cliOptions struct {
	cfg            dbload.Config
	cases          string
	dimension      string
	relation       string
	name           string
	before         string
	after          string
	inputs         string
	scale          int
	slot           int
	seed           int64
	pairedWorker   bool
	strictBaseline bool
}

func main() {
	debug.SetMemoryLimit(debugMemoryLimit)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := execute(ctx, os.Args[1:])
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: dbload seed|run|explore|compare|report|catalog [flags]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	var opts cliOptions
	registerFlags(flags, &opts)
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if opts.cases != "" {
		opts.cfg.Cases = strings.Split(opts.cases, ",")
	}
	opts.cfg = opts.cfg.Defaults()
	if err := configureSQLiteTemp(command, opts.cfg); err != nil {
		return err
	}
	return dispatch(ctx, command, opts)
}

func configureSQLiteTemp(command string, cfg dbload.Config) error {
	switch command {
	case "seed", "run", "explore", "compare", "prepare":
	default:
		return nil
	}
	root := cfg.ResourceRoot
	if root == "" {
		root = cfg.OutputDir
	}
	if root != "" {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		if err = os.Setenv("SQLITE_TMPDIR", absolute); err != nil {
			return err
		}
		if err = os.Setenv("TMPDIR", absolute); err != nil {
			return err
		}
	}
	return nil
}

func registerFlags(flags *flag.FlagSet, opts *cliOptions) {
	flags.StringVar(&opts.cfg.OutputDir, "out", "", "new output directory (report: output HTML file)")
	flags.StringVar(&opts.cfg.ResourceRoot, "resource-root", "", "owned campaign root for peak disk accounting")
	flags.StringVar(&opts.cfg.Registration, "registration", "", "internal: frozen confirmation registration hash")
	flags.StringVar(&opts.cfg.Fixture, "fixture", "", "suite-owned manifest.json")
	flags.StringVar(&opts.cfg.SourceRoot, "source-root", ".", "repository root for source/provenance inspection")
	flags.StringVar(&opts.cases, "cases", "", "comma-separated exact case names; empty = all")
	flags.IntVar(&opts.cfg.Samples, "samples", dbload.DefaultSamples, "attempts per case/block (exploration only)")
	flags.IntVar(&opts.cfg.Blocks, "blocks", dbload.DefaultBlocks, "blocks per case")
	flags.IntVar(&opts.cfg.Concurrency, "concurrency", dbload.DefaultConcurrency, "closed-loop callers:1,4,16")
	flags.StringVar(&opts.cfg.Variant, "variant", "baseline", "recorded binary variant")
	flags.IntVar(&opts.cfg.Pair, "pair", 0, "confirmation pair ID")
	flags.StringVar(&opts.cfg.Order, "order", "AB", "confirmation pair order")
	flags.DurationVar(&opts.cfg.Budget, "budget", dbload.DefaultBudget, "execution budget")
	flags.DurationVar(&opts.cfg.CaseLimit, "case-limit", dbload.DefaultCaseLimit, "per case/block wall cutoff")
	flags.DurationVar(
		&opts.cfg.OperationLimit,
		"operation-limit",
		dbload.DefaultOperationLimit,
		"per attempt cutoff, not a product deadline",
	)
	flags.BoolVar(&opts.cfg.Profile, "profile", false, "diagnostic profiles; never use as benchmark evidence")
	flags.StringVar(&opts.dimension, "dimension", "population", "population,history,fanout,combined")
	flags.StringVar(&opts.relation, "relation", "", "fanout relationship:devices,messages,automations")
	flags.StringVar(&opts.name, "name", "", "scenario name")
	flags.IntVar(&opts.scale, "scale", 1, "scale1,10,100,1000 (combined:10)")
	flags.Int64Var(&opts.seed, "seed", 1, "deterministic fixture seed")
	flags.StringVar(&opts.before, "baseline", "", "frozen baseline executable")
	flags.StringVar(&opts.after, "candidate", "", "candidate executable")
	flags.IntVar(&opts.slot, "slot", 1, "preregistered family slot1..3")
	flags.BoolVar(&opts.pairedWorker, "paired-worker", false, "internal: parent already accounts for execution budget")
	flags.BoolVar(&opts.strictBaseline, "strict-baseline", false, "internal: require the canonical baseline schema")
	flags.StringVar(&opts.inputs, "inputs", "", "comma-separated results.json paths/directories for report")
}

func dispatch(ctx context.Context, command string, opts cliOptions) error {
	switch command {
	case "seed":
		return seedCommand(ctx, opts)
	case "run":
		return runCommand(ctx, opts)
	case "explore":
		_, err := dbload.Explore(ctx, opts.cfg, nil)
		return err
	case "compare":
		return compareCommand(ctx, opts)
	case "prepare":
		_, err := dbload.PrepareVariant(ctx, opts.cfg, opts.strictBaseline)
		return err
	case "report":
		return reportCommand(opts)
	case "catalog":
		return catalogCommand(opts)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func seedCommand(ctx context.Context, opts cliOptions) error {
	name := opts.name
	if name == "" {
		name = fmt.Sprintf("%s-%d", opts.dimension, opts.scale)
		if opts.relation != "" {
			name = fmt.Sprintf("%s-%s-%d", opts.dimension, opts.relation, opts.scale)
		}
	}
	scenario := dbload.Scenario{
		Name:        name,
		Dimension:   opts.dimension,
		Relation:    opts.relation,
		Scale:       opts.scale,
		Seed:        opts.seed,
		Concurrency: 1,
	}
	if err := dbload.ValidateScenario(scenario); err != nil {
		return err
	}
	return dbload.WithBudget(ctx, opts.cfg.ResourceRoot, opts.cfg.Budget, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, opts.cfg.Budget)
		defer cancel()
		m, err := dbload.Seed(ctx, opts.cfg.OutputDir, scenario)
		if err != nil {
			return err
		}
		manifestPath := filepath.Join(opts.cfg.OutputDir, "manifest.json")
		if err = dbload.WriteJSON(manifestPath, m); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, manifestPath)
		return nil
	})
}

func runCommand(ctx context.Context, opts cliOptions) error {
	action := func(ctx context.Context) error { _, err := dbload.Run(ctx, opts.cfg); return err }
	if opts.pairedWorker {
		return action(ctx)
	}
	return dbload.WithBudget(ctx, opts.cfg.ResourceRoot, opts.cfg.Budget, action)
}

func compareCommand(ctx context.Context, opts cliOptions) error {
	return dbload.WithBudget(
		ctx,
		opts.cfg.ResourceRoot,
		opts.cfg.Budget,
		func(ctx context.Context) error {
			_, err := dbload.Compare(ctx, opts.cfg, opts.before, opts.after, opts.slot)
			return err
		},
	)
}

func reportCommand(opts cliOptions) error {
	if opts.inputs == "" || opts.cfg.OutputDir == "" {
		return errors.New("report requires -inputs and -out")
	}
	paths, err := dbload.ResultFiles(strings.Split(opts.inputs, ","))
	if err != nil {
		return err
	}
	return dbload.Report(paths, opts.cfg.OutputDir)
}

func catalogCommand(opts cliOptions) error {
	items, _, err := dbload.Catalog(opts.cfg.SourceRoot)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(items)
}
