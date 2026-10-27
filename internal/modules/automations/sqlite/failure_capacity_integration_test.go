//go:build integration

package sqlite_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Condition evaluations retain their independent pre-expression history budget;
// value failures retain every diagnostic without charging that history twice.
//
//nolint:gocognit // The two concrete boundary regressions share admission, atomic failure and complete-diagnostic oracles.
func TestValueFailureCapacityRetainsCompleteDiagnostics(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"escaped State pointer", "Choose prefix"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			db := openAutomationDatabase(t)
			repo := newAutomationRepository(t, db)
			at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
			definition := validDomainDefinition(t)
			var evidence automations.InitialStepEvidence
			if kind == "escaped State pointer" {
				entity := newEntityID(t)
				pointer := "/" + strings.Repeat("\x01", 255)
				definition.Steps = []automations.Step{{ID: "failed", Body: automations.ReadStateStep{
					EntityID: entity, Pointer: pointer, OnUnusable: automations.ReadStateFail,
				}}}
				evidence = automations.ValueResolutionFailed{Kind: automations.StepKindReadState, At: at,
					Failure: automations.ValueFailure{
						Path:        "/steps/0",
						FailureCode: "read_state_unusable",
						State: &automations.StateReadFailure{
							EntityID:       entity,
							Pointer:        pointer,
							ReadAt:         at,
							UnusableReason: automations.ConditionUnknownEntityMissing,
						},
					}}
			} else {
				entity := newEntityID(t)
				observation := automations.ObservationEvidence{
					ObservationID: devices.ObservationID(newObservationIDString(t)),
					ObservedAt:    at,
				}
				children := make([]automations.Condition, 63)
				nodes := make([]automations.ConditionNodeResult, 63)
				selected := json.RawMessage(`"` + strings.Repeat("x", 40000) + `"`)
				for i := range children {
					id := automations.ConditionID(fmt.Sprintf("state_%d", i))
					children[i] = automations.Condition{ID: id, Body: automations.EntityStateCondition{
						EntityID: entity, Operator: automations.ComparisonEqual, Operand: json.RawMessage(`""`),
					}}
					nodes[i] = automations.ConditionNodeResult{ID: id, Evidence: automations.KnownStateEvidence{
						Matched: false, Observation: observation, SelectedValue: selected,
					}}
				}
				definition.Steps = []automations.Step{
					{ID: "failed", Body: automations.ChooseStep{Branches: []automations.ChooseBranch{
						{
							ID: "first",
							Conditions: automations.Condition{
								ID:   "all",
								Body: automations.AllCondition{Children: children},
							},
							Steps: []automations.Step{{ID: "wait_a", Body: automations.DelayStep{DurationMS: 1}}},
						},
						{
							ID: "second",
							Conditions: automations.Condition{ID: "bad", Body: automations.ExpressionCondition{
								Value: automations.ValueBinding{
									Body: automations.ExpressionValue{Source: "vars.missing"},
								},
							}},
							Steps: []automations.Step{{ID: "wait_b", Body: automations.DelayStep{DurationMS: 1}}},
						},
					}}},
				}
				evidence = automations.BranchDecided{
					Decision: automations.BranchDecision{
						StepID:      "failed",
						EvaluatedAt: at,
						Body: automations.ChooseDecision{
							Result: automations.ChooseError{
								FailureCode: "expression_missing",
								Evaluations: []automations.ChooseEvaluation{
									{
										BranchID: "first",
										Evaluation: automations.ConditionEvaluation{
											EvaluatedAt: at,
											Result:      automations.ConditionFalse,
											Nodes:       nodes,
										},
									},
								},
							},
						},
					},
					ValueFailure: &automations.ValueFailure{
						Path:        "/steps/0/branches/1/conditions/expr",
						FailureCode: "expression_missing",
					},
				}
			}
			record, run := admitStorageRun(t, repo, definition, at)
			reached := automations.StepExecutionRecord{RunID: run.ID, StepID: "failed", Evidence: evidence}
			details, _, err := automations.PrepareStepExecutionDetails(reached)
			if err != nil {
				t.Fatal(err)
			}
			if err = automations.ValidateStepValueOwnershipWithPreparedSnapshot(
				automations.StepExecution{StepID: "failed", Details: details},
				run.Snapshot,
				true,
			); err != nil {
				t.Fatal(err)
			}
			public, err := automations.EncodePublicStepExecution(
				automations.StepExecution{StepID: "failed", Details: details},
			)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "escaped State pointer" {
				if len(public) <= 1024 || len(public) > 4096 {
					t.Fatalf("escaped State failure needs %d bytes", len(public))
				}
				// Seed only the counter to isolate the reserved byte boundary. This
				// fixture intentionally does not manufacture unrelated old payloads.
				mustExec(
					t,
					db,
					`UPDATE automation_history SET value_evidence_bytes=? WHERE id=?`,
					2*1024*1024-4096,
					string(run.ID),
				)
			} else if len(public) <= 2*1024*1024 {
				t.Fatalf("Choose diagnostic does not exceed the value budget: %d bytes", len(public))
			}
			err = repo.RecordStep(t.Context(), reached)
			if err != nil {
				t.Fatalf("valid %s failure (%d bytes) rejected: %v", kind, len(public), err)
			}
			var status, stored string
			var bytes, records int
			if err = db.QueryRow(`SELECT h.run_status,h.value_evidence_bytes,h.value_evidence_records,e.evidence_json FROM automation_history h JOIN automation_step_executions e ON e.run_id=h.id WHERE h.id=?`, string(run.ID)).
				Scan(&status, &bytes, &records, &stored); err != nil {
				t.Fatal(err)
			}
			_, envelope, err := automations.PrepareStepExecutionDetails(reached)
			if err != nil {
				t.Fatal(err)
			}
			if status != "failed" || bytes > 2*1024*1024 || records != 2 || stored != string(envelope.EvidenceJSON) {
				t.Fatalf("incomplete failure: status=%s bytes=%d records=%d", status, bytes, records)
			}
			if kind == "Choose prefix" {
				retained := retainedStorageRun(t, repo, record, run)
				retainedPublic, encodeErr := automations.EncodePublicStepExecution(retained.Executions[0])
				if encodeErr != nil || string(retainedPublic) != string(public) {
					t.Fatalf("Choose diagnostics changed: %v", encodeErr)
				}
				if bytes > 4096 {
					t.Fatalf("Condition evaluations charged as expression values: %d", bytes)
				}
			}
		})
	}
}
