// Command agenteval evaluates the built-in Agent through real Core against
// the scripted simulator. See docs/guides/agent-evaluation.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/agenteval"
)

const (
	defaultScenarios        = "testdata/agenteval/scenarios"
	defaultHousehold        = "testdata/agenteval/household.simulator.yaml"
	defaultLabels           = "testdata/agenteval/judge-labels.jsonl"
	defaultTrials           = 10
	defaultRunMaxRequests   = 3000
	defaultTrialMaxRequests = 15
	defaultMinGap           = 15 * time.Second
	defaultRateLimitPause   = 2 * time.Minute
	refUsage                = "git ref, or worktree"
	outFlag                 = "out"
	minGapFlag              = "min-gap"
	scenariosFlag           = "scenarios"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "agenteval:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newCommand(os.Stdout, os.Stderr).Run(ctx, os.Args)
}

func newCommand(stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:   "agenteval",
		Usage:  "evaluate the built-in Agent through real Core and the scripted simulator",
		Writer: stdout, ErrWriter: stderr,
		Commands: []*cli.Command{
			{
				Name: "protocol", Usage: "print the worker protocol version",
				Action: func(_ context.Context, _ *cli.Command) error {
					return json.NewEncoder(stdout).Encode(map[string]int{"protocol": agenteval.WorkerProtocol})
				},
			},
			workerCommand(),
			runCommand("compare", "compare two revisions or model settings and report a verdict", true, stdout, stderr),
			runCommand("run", "run one revision and report its results without a verdict", false, stdout, stderr),
			calibrateCommand(stdout),
			{
				Name: "report", Usage: "rebuild summary.json and report.html from a run directory",
				ArgsUsage: "DIR",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "rescore",
						Usage: "score every trial again from records/ with the current checks",
					},
					&cli.StringFlag{
						Name:  "scenarios",
						Value: defaultScenarios,
						Usage: "scenario files the run used, for --rescore",
					},
				},
				Action: func(_ context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 1 {
						return errors.New("report needs one run directory")
					}
					dir := cmd.Args().First()
					if cmd.Bool("rescore") {
						scenarios, err := agenteval.LoadScenarios(cmd.String(scenariosFlag))
						if err != nil {
							return err
						}
						if err = agenteval.Rescore(dir, scenarios); err != nil {
							return err
						}
					}
					return writeReport(dir, stdout)
				},
			},
		},
	}
}

func workerCommand() *cli.Command {
	return &cli.Command{
		Name: "worker", Usage: "run one scenario trial and write its record (used by the driver)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "scenario", Required: true},
			&cli.StringFlag{Name: "household", Required: true},
			&cli.IntFlag{Name: "trial"},
			&cli.StringFlag{Name: outFlag, Required: true},
			&cli.StringFlag{Name: "dir", Required: true},
			&cli.StringFlag{Name: "revision"},
			&cli.StringFlag{Name: "provider"},
			&cli.StringFlag{Name: "model"},
			&cli.StringFlag{Name: "reasoning"},
			&cli.StringFlag{Name: "base-url"},
			&cli.IntFlag{Name: "max-requests"},
			&cli.DurationFlag{Name: minGapFlag},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			scenario, err := agenteval.LoadScenario(cmd.String("scenario"))
			if err != nil {
				return err
			}
			record := agenteval.RunTrial(ctx, agenteval.TrialOptions{
				Scenario: scenario, Trial: cmd.Int("trial"), SimulatorConfig: cmd.String("household"),
				Settings: agenteval.ModelSettings{
					Provider: cmd.String("provider"), Model: cmd.String("model"),
					ReasoningEffort: cmd.String("reasoning"), BaseURL: cmd.String("base-url"),
				},
				MaxRequests: cmd.Int("max-requests"), MinGap: cmd.Duration(minGapFlag),
				Dir: cmd.String("dir"), Revision: cmd.String("revision"),
			})
			raw, err := json.Marshal(record)
			if err != nil {
				return err
			}
			return os.WriteFile(cmd.String(outFlag), raw, 0o600)
		},
	}
}

func runFlags(compare bool) []cli.Flag {
	flags := []cli.Flag{
		&cli.StringFlag{Name: scenariosFlag, Value: defaultScenarios},
		&cli.StringFlag{Name: "household", Value: defaultHousehold},
		&cli.StringSliceFlag{Name: "tags", Usage: "run only scenarios with these tags or IDs"},
		&cli.IntFlag{Name: "trials", Value: defaultTrials},
		&cli.StringFlag{Name: "model", Usage: "provider/model for every arm, e.g. opencode-go/glm-5.3", Required: true},
		&cli.StringFlag{Name: "reasoning", Usage: "reasoning effort for every arm"},
		&cli.StringFlag{Name: "base-url", Usage: "provider endpoint override for every arm"},
		&cli.IntFlag{Name: "max-requests", Value: defaultRunMaxRequests, Usage: "model-request cap for the whole run"},
		&cli.IntFlag{
			Name: "trial-max-requests", Value: defaultTrialMaxRequests,
			Usage: "default per-trial cap; scenarios may override",
		},
		&cli.DurationFlag{Name: minGapFlag, Value: defaultMinGap, Usage: "minimum time between model requests"},
		&cli.DurationFlag{Name: "rate-limit-pause", Value: defaultRateLimitPause},
		&cli.StringFlag{Name: outFlag, Usage: "run directory (default .data/agenteval/<UTC time>)"},
		&cli.BoolFlag{Name: "resume", Usage: "continue the run in --out"},
		&cli.IntFlag{Name: "regression-drop", Usage: "override the regression threshold"},
		&cli.IntFlag{Name: "min-pairs", Usage: "override the completed-pairs threshold"},
		&cli.StringFlag{
			Name:  "judge-model",
			Usage: "provider/model that grades rubrics, report-only; must differ from every arm",
		},
		&cli.StringFlag{Name: "judge-base-url", Usage: "provider endpoint override for the judge"},
	}
	if !compare {
		return append(flags, &cli.StringFlag{Name: "revision", Value: agenteval.WorktreeRef, Usage: refUsage})
	}
	return append(flags,
		&cli.StringFlag{Name: "baseline", Required: true, Usage: refUsage},
		&cli.StringFlag{Name: "candidate", Required: true, Usage: refUsage},
		&cli.StringFlag{Name: "baseline-model", Usage: "provider/model override for the baseline arm"},
		&cli.StringFlag{Name: "candidate-model", Usage: "provider/model override for the candidate arm"},
	)
}

func runCommand(name, usage string, compare bool, stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name: name, Usage: usage, Flags: runFlags(compare),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			options, err := runOptions(cmd, compare, stderr)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(stdout, "run directory:", options.Out)
			summary, err := agenteval.Run(ctx, options)
			if err != nil {
				return err
			}
			if err = writeReport(options.Out, stdout); err != nil {
				return err
			}
			if summary.Verdict != "" {
				_, _ = fmt.Fprintf(stdout, "verdict: %s\n", summary.Verdict)
				for _, reason := range summary.Reasons {
					_, _ = fmt.Fprintln(stdout, "  -", reason)
				}
			}
			return nil
		},
	}
}

func runOptions(cmd *cli.Command, compare bool, stderr io.Writer) (agenteval.RunOptions, error) {
	repo, err := os.Getwd()
	if err != nil {
		return agenteval.RunOptions{}, err
	}
	arms, err := runArms(cmd, compare)
	if err != nil {
		return agenteval.RunOptions{}, err
	}
	out := cmd.String(outFlag)
	if out == "" {
		out = filepath.Join(".data", "agenteval", time.Now().UTC().Format("20060102T150405Z"))
	}
	if out, err = filepath.Abs(out); err != nil {
		return agenteval.RunOptions{}, err
	}
	household, err := filepath.Abs(cmd.String("household"))
	if err != nil {
		return agenteval.RunOptions{}, err
	}
	rule := agenteval.DefaultRule(cmd.Int("trials"))
	if value := cmd.Int("regression-drop"); value > 0 {
		rule.RegressionDrop = value
	}
	if value := cmd.Int("min-pairs"); value > 0 {
		rule.MinPairs = value
	}
	var judge *agenteval.Judge
	if value := cmd.String("judge-model"); value != "" {
		settings, parseErr := parseModel(value, "", cmd.String("judge-base-url"))
		if parseErr != nil {
			return agenteval.RunOptions{}, parseErr
		}
		if err = agenteval.CheckJudgeModel(settings, arms); err != nil {
			return agenteval.RunOptions{}, err
		}
		if judge, err = agenteval.NewJudge(settings); err != nil {
			return agenteval.RunOptions{}, err
		}
	}
	return agenteval.RunOptions{
		Judge: judge, Repo: repo, Arms: arms, ScenarioDir: cmd.String(scenariosFlag), SimulatorConfig: household,
		Tags: cmd.StringSlice("tags"), Trials: cmd.Int("trials"),
		TrialMaxRequests: cmd.Int("trial-max-requests"), RunMaxRequests: cmd.Int("max-requests"),
		MinGap: cmd.Duration(minGapFlag), RateLimitPause: cmd.Duration("rate-limit-pause"),
		Out: out, Resume: cmd.Bool("resume"), Rule: rule, Log: stderr,
	}, nil
}

func runArms(cmd *cli.Command, compare bool) ([]agenteval.Arm, error) {
	model := func(flag string) (agenteval.ModelSettings, error) {
		value := cmd.String(flag)
		if value == "" {
			value = cmd.String("model")
		}
		return parseModel(value, cmd.String("reasoning"), cmd.String("base-url"))
	}
	if !compare {
		settings, err := model("model")
		return []agenteval.Arm{{Name: agenteval.ArmCandidate, Ref: cmd.String("revision"), Settings: settings}}, err
	}
	baseline, err := model("baseline-model")
	if err != nil {
		return nil, err
	}
	candidate, err := model("candidate-model")
	return []agenteval.Arm{
		{Name: agenteval.ArmBaseline, Ref: cmd.String("baseline"), Settings: baseline},
		{Name: agenteval.ArmCandidate, Ref: cmd.String("candidate"), Settings: candidate},
	}, err
}

func calibrateCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name: "calibrate", Usage: "grade hand-labeled conversations and report the judge's agreement per criterion",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "judge-model", Required: true, Usage: "provider/model to calibrate"},
			&cli.StringFlag{Name: "judge-base-url", Usage: "provider endpoint override for the judge"},
			&cli.StringFlag{Name: "labels", Value: defaultLabels},
			&cli.StringFlag{Name: scenariosFlag, Value: defaultScenarios},
			&cli.StringFlag{Name: outFlag, Usage: "also write the calibration as JSON"},
			&cli.DurationFlag{Name: minGapFlag, Value: defaultMinGap, Usage: "minimum time between judge requests"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			settings, err := parseModel(cmd.String("judge-model"), "", cmd.String("judge-base-url"))
			if err != nil {
				return err
			}
			labels, err := agenteval.LoadLabels(cmd.String("labels"))
			if err != nil {
				return err
			}
			scenarios, err := agenteval.LoadScenarios(cmd.String(scenariosFlag))
			if err != nil {
				return err
			}
			if err = agenteval.CheckLabels(labels, scenarios); err != nil {
				return err
			}
			judge, err := agenteval.NewJudge(settings)
			if err != nil {
				return err
			}
			result := agenteval.Calibrate(ctx, judge, labels, cmd.Duration(minGapFlag))
			if err = ctx.Err(); err != nil {
				return err
			}
			printCalibration(stdout, result)
			if out := cmd.String(outFlag); out != "" {
				raw, _ := json.MarshalIndent(result, "", "  ")
				return os.WriteFile(out, raw, 0o600)
			}
			return nil
		},
	}
}

func printCalibration(stdout io.Writer, result agenteval.Calibration) {
	_, _ = fmt.Fprintf(stdout, "judge %s/%s: %d of %d labeled grades agree, %d unsure, %d failed requests\n\n",
		result.Judge.Provider, result.Judge.Model, result.Agreed, result.Labeled, result.Unsure, result.Errors)
	for _, item := range result.Criteria {
		_, _ = fmt.Fprintf(stdout, "%2d/%-2d agree  %d unsure  %-28s %s\n",
			item.Agreed, item.Labeled, item.Unsure, item.Scenario, item.Criterion)
	}
	if len(result.Mismatches) > 0 {
		_, _ = fmt.Fprintln(stdout, "\ndisagreements:")
		for _, mismatch := range result.Mismatches {
			_, _ = fmt.Fprintln(stdout, "  -", mismatch)
		}
	}
}

func parseModel(value, reasoning, baseURL string) (agenteval.ModelSettings, error) {
	provider, model, ok := strings.Cut(value, "/")
	if !ok || provider == "" || model == "" {
		return agenteval.ModelSettings{}, fmt.Errorf("model %q must be provider/model", value)
	}
	return agenteval.ModelSettings{Provider: provider, Model: model, ReasoningEffort: reasoning, BaseURL: baseURL}, nil
}

func writeReport(dir string, stdout io.Writer) error {
	path, err := agenteval.WriteReport(dir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "report:", path)
	return nil
}
