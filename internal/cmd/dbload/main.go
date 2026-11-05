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
	"time"

	"github.com/mholtzscher/hearth/internal/dbload"
)

func main() {
	debug.SetMemoryLimit(4 << 30)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, os.Args[1:]); err != nil {
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
	var cfg dbload.Config
	var cases, dimension, relation, name, before, after, inputs string
	var scale, slot int
	var seed int64
	var pairedWorker bool
	flags.StringVar(&cfg.OutputDir, "out", "", "new output directory (report: output HTML file)")
	flags.StringVar(&cfg.ResourceRoot, "resource-root", "", "owned campaign root for peak disk accounting")
	flags.StringVar(&cfg.Registration, "registration", "", "internal: frozen confirmation registration hash")
	flags.StringVar(&cfg.Fixture, "fixture", "", "suite-owned manifest.json")
	flags.StringVar(&cfg.SourceRoot, "source-root", ".", "repository root for source/provenance inspection")
	flags.StringVar(&cases, "cases", "", "comma-separated exact case names; empty = all")
	flags.IntVar(&cfg.Samples, "samples", 20, "attempts per case/block (exploration only)")
	flags.IntVar(&cfg.Blocks, "blocks", 3, "blocks per case")
	flags.IntVar(&cfg.Concurrency, "concurrency", 1, "closed-loop callers:1,4,16")
	flags.StringVar(&cfg.Variant, "variant", "baseline", "recorded binary variant")
	flags.IntVar(&cfg.Pair, "pair", 0, "confirmation pair ID")
	flags.StringVar(&cfg.Order, "order", "AB", "confirmation pair order")
	flags.DurationVar(&cfg.Budget, "budget", 2*time.Hour, "execution budget")
	flags.DurationVar(&cfg.CaseLimit, "case-limit", time.Minute, "per case/block wall cutoff")
	flags.DurationVar(
		&cfg.OperationLimit,
		"operation-limit",
		10*time.Second,
		"per attempt cutoff, not a product deadline",
	)
	flags.BoolVar(&cfg.Profile, "profile", false, "diagnostic profiles; never use as benchmark evidence")
	flags.StringVar(&dimension, "dimension", "population", "population,history,fanout,combined")
	flags.StringVar(&relation, "relation", "", "fanout relationship:devices,messages,automations")
	flags.StringVar(&name, "name", "", "scenario name")
	flags.IntVar(&scale, "scale", 1, "scale1,10,100,1000 (combined:10)")
	flags.Int64Var(&seed, "seed", 1, "deterministic fixture seed")
	flags.StringVar(&before, "baseline", "", "frozen baseline executable")
	flags.StringVar(&after, "candidate", "", "candidate executable")
	flags.IntVar(&slot, "slot", 1, "preregistered family slot1..3")
	flags.BoolVar(&pairedWorker, "paired-worker", false, "internal: parent already accounts for execution budget")
	flags.StringVar(&inputs, "inputs", "", "comma-separated results.json paths/directories for report")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if cases != "" {
		cfg.Cases = strings.Split(cases, ",")
	}
	cfg = cfg.Defaults()
	switch command {
	case "seed":
		if name == "" {
			name = fmt.Sprintf("%s-%d", dimension, scale)
			if relation != "" {
				name = fmt.Sprintf("%s-%s-%d", dimension, relation, scale)
			}
		}
		scenario := dbload.Scenario{
			Name:        name,
			Dimension:   dimension,
			Relation:    relation,
			Scale:       scale,
			Seed:        seed,
			Concurrency: 1,
		}
		if err := dbload.ValidateScenario(scenario); err != nil {
			return err
		}
		return dbload.WithBudget(ctx, cfg.ResourceRoot, cfg.Budget, func(ctx context.Context) error {
			m, err := dbload.Seed(ctx, cfg.OutputDir, scenario)
			if err != nil {
				return err
			}
			if err = dbload.WriteJSON(filepath.Join(cfg.OutputDir, "manifest.json"), m); err != nil {
				return err
			}
			fmt.Println(filepath.Join(cfg.OutputDir, "manifest.json"))
			return nil
		})
	case "run":
		action := func(ctx context.Context) error { _, err := dbload.Run(ctx, cfg); return err }
		if pairedWorker {
			return action(ctx)
		}
		return dbload.WithBudget(ctx, cfg.ResourceRoot, cfg.Budget, action)
	case "explore":
		_, err := dbload.Explore(ctx, cfg, nil)
		return err
	case "compare":
		return dbload.WithBudget(
			ctx,
			cfg.ResourceRoot,
			cfg.Budget,
			func(ctx context.Context) error { _, err := dbload.Compare(ctx, cfg, before, after, slot); return err },
		)
	case "report":
		if inputs == "" || cfg.OutputDir == "" {
			return errors.New("report requires -inputs and -out")
		}
		paths, err := dbload.ResultFiles(strings.Split(inputs, ","))
		if err != nil {
			return err
		}
		return dbload.Report(paths, cfg.OutputDir)
	case "catalog":
		items, _, err := dbload.Catalog(cfg.SourceRoot)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(items)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}
