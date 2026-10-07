package sqlite_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestHistoryRejectsObservationEvidenceContradictingMatchedTrigger(t *testing.T) {
	t.Parallel()
	for _, skip := range []bool{false, true} {
		name := "Run"
		if skip {
			name = "Skip"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, test := range []struct {
				name, column, value string
			}{
				{"disposition", "fact_variant", string(devices.DispositionUnchanged)},
				{"current comparison", "fact_value_json", `{"temperature":15}`},
				{"previous comparison", "fact_previous_value_json", `{"temperature":25}`},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					assertContradictoryObservationHistory(t, skip, test.column, test.value)
				})
			}
		})
	}
}

func assertContradictoryObservationHistory(t *testing.T, skip bool, column, value string) {
	t.Helper()
	database := openAutomationDatabase(t)
	now := admissionNow
	repository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{
		Now: func() time.Time { return now },
	})
	definition := validDomainDefinition(t)
	trigger := definition.Triggers[0].Body.(automations.ObservationTrigger)
	trigger.PreviousComparisons = []automations.ObservationComparison{{
		Pointer: "/temperature", Operator: automations.ComparisonLessThan, Operand: json.RawMessage("20"),
	}}
	definition.Triggers[0].Body = trigger
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	fact := observationFactFor(t, trigger.EntityID, admissionNow)
	fact.PreviousValue = devices.Value(`{"temperature":10}`)
	admittedAt := admissionNow
	if skip {
		admittedAt = admissionNow.Add(automations.FactMaximumAge + time.Second)
	}
	admitted, err := repository.AdmitDeviceFact(
		t.Context(), fact, devices.EntityStateSnapshot{}, admittedAt, admissionNow.Add(-time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	var entryID string
	if skip {
		if len(admitted.Skips) != 1 {
			t.Fatalf("admission = %#v, want Skip", admitted)
		}
		entryID = string(admitted.Skips[0].SkipID)
	} else {
		if len(admitted.StartedRuns) != 1 {
			t.Fatalf("admission = %#v, want Run", admitted)
		}
		entryID = string(admitted.StartedRuns[0].ID)
	}
	// Retained matching is structural, even when the Fact is now very old.
	now = admissionNow.Add(365 * 24 * time.Hour)
	if _, err = repository.GetHistoryEntry(t.Context(), record.ID, entryID); err != nil {
		t.Fatalf("valid historical Fact = %v", err)
	}
	mustExec(t, database, "UPDATE automation_history SET "+column+" = ? WHERE id = ?", value, entryID)
	if _, err = repository.GetHistoryEntry(
		t.Context(),
		record.ID,
		entryID,
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("contradictory %s evidence = %v, want invalid Automation", column, err)
	}
}
