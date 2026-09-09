package automations //nolint:testpackage // Tests management semantics against the device catalog and SQLite.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// A4: management validates every target through the real catalog without
// dispatching, and does not confuse temporary control eligibility with support.
//
//nolint:paralleltest,tparallel // Shared database assertions follow the invalid mutation group.
func TestAutomationManagementSemanticValidation(
	t *testing.T,
) {
	t.Parallel()
	harness := newAutomationExecutionHarness(t)
	if _, err := harness.devices.SetEntityEnabled(t.Context(), harness.power, false); err != nil {
		t.Fatal(err)
	}
	valid := harness.definition()
	valid.Name = "  Trim me  "
	valid.Triggers[0].Expression = "  0  19\t* * *  "
	valid.Triggers = append(
		valid.Triggers,
		AutomationTrigger{ID: "same-expression", Kind: AutomationTriggerKindCron, Expression: "0 19 * * *"},
	)
	record := createExecutionAutomation(t, harness, valid)
	if record.Definition.Name != "Trim me" || record.Definition.Enabled ||
		record.Definition.Triggers[0].Expression != "0 19 * * *" ||
		record.Revision != 1 ||
		harness.sends.Load() != 0 {
		t.Fatalf("normalized definition = %#v", record)
	}
	cases := map[string]func(*AutomationDefinition){
		"blank-name": func(d *AutomationDefinition) { d.Name = " \t " },
		"duplicate-trigger": func(d *AutomationDefinition) {
			d.Triggers = append(d.Triggers, d.Triggers[0])
			d.Triggers[1].Expression = "1 19 * * *"
		},
		"cron":                  func(d *AutomationDefinition) { d.Triggers[0].Expression = "@hourly" },
		"unknown-entity":        func(d *AutomationDefinition) { d.Steps[1].EntityID = "ent_01900000-0000-7000-8000-000000000099" },
		"invalid-entity":        func(d *AutomationDefinition) { d.Steps[1].EntityID = "secret-input" },
		"unsupported-operation": func(d *AutomationDefinition) { d.Steps[1].OperationName = "set" },
		"unsupported-parameters": func(d *AutomationDefinition) {
			d.Steps[1].Parameters = devices.CommandParameters(`{"name":"secret-input"}`)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			definition := harness.definition()
			mutate(&definition)
			if _, err := harness.service.CreateAutomation(
				t.Context(),
				definition,
			); !errors.Is(err, ErrInvalidAutomation) ||
				strings.Contains(err.Error(), "secret-input") {
				t.Fatalf("semantic error = %v", err)
			}
			if _, err := harness.service.UpdateAutomation(
				t.Context(),
				AutomationUpdate{ID: record.ID, ExpectedRevision: 1, Definition: definition},
			); !errors.Is(
				err,
				ErrInvalidAutomation,
			) {
				t.Fatalf("invalid update = %v", err)
			}
		})
	}
	page, err := harness.service.ListAutomations(t.Context(), AutomationListParams{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Revision != 1 || harness.sends.Load() != 0 {
		t.Fatalf("invalid management wrote or dispatched: %#v, %v", page, err)
	}
	if _, err = harness.service.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: record.ID, ExpectedRevision: 2, Definition: harness.definition()},
	); !errors.Is(
		err,
		ErrAutomationRevisionConflict,
	) {
		t.Fatalf("stale update = %v", err)
	}
	if err = harness.service.DeleteAutomation(
		t.Context(),
		record.ID,
		2,
	); !errors.Is(
		err,
		ErrAutomationRevisionConflict,
	) {
		t.Fatalf("stale delete = %v", err)
	}
	admission := startExecutionAutomation(t, harness, record.ID, "disabled-target")
	run := waitExecutionRun(t, harness, admission.Run.ID)
	if run.Status != AutomationRunStatusFailed || run.Steps[0].CommandID == nil || run.Steps[0].FailureCode == nil ||
		*run.Steps[0].FailureCode != "entity_disabled" ||
		run.Steps[1].Status != AutomationStepStatusNotAttempted ||
		harness.sends.Load() != 0 {
		t.Fatalf("disabled target failure = %#v", run)
	}
}

type automationCommandsOverride struct {
	AutomationCommands

	execute func(context.Context, devices.CommandInput) (devices.CommandResult, error)
}

func (commands automationCommandsOverride) ExecuteCommand(
	ctx context.Context,
	input devices.CommandInput,
) (devices.CommandResult, error) {
	return commands.execute(ctx, input)
}

type automationRecordsOverride struct {
	read func(context.Context, devices.CommandID) (devices.CommandRecord, error)
}

func (records automationRecordsOverride) GetCommand(
	ctx context.Context,
	id devices.CommandID,
) (devices.CommandRecord, error) {
	return records.read(ctx, id)
}

// A5: returned errors cannot override owned durable terminal outcomes, and
// unreadable/foreign evidence cannot be transformed into terminal failure.
//
//nolint:gocognit // Each matrix row checks a distinct durable-evidence boundary.
func TestAutomationAuthoritativeReconciliation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"terminal-observed", "terminal-dispatched", "unreadable", "foreign", "not-found", "invalid-command", "entity-not-found"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			harness := newAutomationExecutionHarness(t)
			definition := harness.definition()
			if mode == "terminal-dispatched" {
				definition.Steps[0] = definition.Steps[1]
			}
			original := harness.devices
			harness.service.commands = automationCommandsOverride{
				AutomationCommands: original,
				execute: func(ctx context.Context, input devices.CommandInput) (devices.CommandResult, error) {
					switch mode {
					case "not-found":
						return devices.CommandResult{}, errors.New("private database details")
					case "invalid-command":
						return devices.CommandResult{}, devices.ErrInvalidCommand
					case "entity-not-found":
						return devices.CommandResult{}, devices.ErrEntityNotFound
					}
					result, err := original.ExecuteCommand(ctx, input)
					if err != nil {
						return result, err
					}
					if mode == "foreign" {
						if _, err = harness.database.ExecContext(
							ctx,
							`UPDATE commands SET correlation_id = ? WHERE id = ?`,
							"cor_01890f47-7a6b-7c4d-8e9f-0123456789ad",
							input.ID,
						); err != nil {
							return devices.CommandResult{}, err
						}
					}
					return devices.CommandResult{}, &devices.CommandExecutionError{
						CommandID: input.ID,
						Err:       errors.New("ambiguous completion"),
					}
				},
			}
			if mode == "unreadable" {
				harness.service.commandRecords = automationRecordsOverride{
					read: func(context.Context, devices.CommandID) (devices.CommandRecord, error) {
						return devices.CommandRecord{}, errors.New("private read failure")
					},
				}
			}
			automation := createExecutionAutomation(t, harness, definition)
			admission := startExecutionAutomation(t, harness, automation.ID, "reconciliation")
			run := waitExecutionRun(t, harness, admission.Run.ID)
			if mode == "foreign" {
				assertAutomationNoCommandEvidence(t, run.Steps[0])
			}
			switch mode {
			case "terminal-observed", "terminal-dispatched":
				if run.Status != AutomationRunStatusSucceeded || harness.sends.Load() != 2 ||
					!harness.service.AutomationExecutionReady() {
					t.Fatalf("terminal outcome not authoritative: %#v", run)
				}
			case "unreadable", "foreign":
				if run.Status != AutomationRunStatusRunning || run.Steps[1].Status != AutomationStepStatusPending ||
					harness.sends.Load() != 1 ||
					harness.service.AutomationExecutionReady() {
					t.Fatalf("uncertainty lost active claim: %#v", run)
				}
			default:
				code := AutomationFailureInternalError
				if mode == "invalid-command" {
					code = AutomationFailureInvalidCommand
				}
				if mode == "entity-not-found" {
					code = AutomationFailureEntityNotFound
				}
				if run.Status != AutomationRunStatusFailed || run.Steps[0].CommandID != nil || run.FailureCode == nil ||
					*run.FailureCode != code ||
					run.Steps[1].Status != AutomationStepStatusNotAttempted ||
					harness.sends.Load() != 0 ||
					!harness.service.AutomationExecutionReady() {
					t.Fatalf("established pre-creation failure = %#v", run)
				}
			}
		})
	}
}

// A5: execution revalidates current support rather than trusting the saved DSL.
func TestAutomationExecutionRechecksSupportAfterEarlierStep(t *testing.T) {
	t.Parallel()
	harness := newAutomationExecutionHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	harness.beforeSend = func(context.Context, devices.CommandRequest) error { close(entered); <-release; return nil }
	automation := createExecutionAutomation(t, harness, harness.definition())
	admission := startExecutionAutomation(t, harness, automation.ID, "support-changed")
	receiveAutomationBarrier(t, entered)
	if _, err := harness.database.ExecContext(
		t.Context(),
		`UPDATE entities SET support_json = '{"state":{},"operations":{"trigger":{"values":["other"]}}}' WHERE id = ?`,
		harness.effect,
	); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	run := waitExecutionRun(t, harness, admission.Run.ID)
	if run.Status != AutomationRunStatusFailed || run.Steps[0].Status != AutomationStepStatusSatisfied ||
		run.Steps[1].FailureCode == nil ||
		*run.Steps[1].FailureCode != AutomationFailureInvalidCommand ||
		run.Steps[1].CommandID != nil ||
		harness.sends.Load() != 1 {
		t.Fatalf("execution trusted stale support: %#v", run)
	}
}

func assertAutomationNoCommandEvidence(t *testing.T, step AutomationRunStep) {
	t.Helper()
	if step.CommandID != nil || step.CommandStatus != nil || step.Outcome != nil {
		t.Fatalf("history adopted unowned command evidence: %#v", step)
	}
}
