//go:build integration

package nats //nolint:testpackage // Exercises private disposition with an actual stored-data failure.

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func TestDeviceFactDispositionRetainsFactAfterStoredFactCorruption(t *testing.T) {
	t.Parallel()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "corrupt.db"))
	repository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
	definition, err := automations.DecodeDefinition(
		json.RawMessage(
			`{"name":"Corruption fixture","enabled":true,"triggers":[{"id":"press","kind":"entity_event","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","event_name":"press"}],"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		devices.EntityStateSnapshot{},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"PRAGMA ignore_check_constraints = ON",
		`UPDATE automation_history SET run_source = 'device_fact', fact_id = 'private', fact_family = 'observation', fact_entity_id = 'private', fact_variant = 'applied', fact_causation_id = 'private', fact_emitted_at = recorded_at, fact_value_json = 'true'`,
	} {
		if _, err = database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	_, storedErr := repository.GetHistoryEntry(t.Context(), record.ID, string(entry.(automations.Run).ID))
	if storedErr == nil {
		t.Fatal("accepted corrupt stored Fact")
	}
	// Feed the concrete persistence failure through the consumer's existing policy.
	receiver := newFakeDeviceFactReceiver()
	receiver.failNext(1, storedErr)
	validator := testValidator(t)
	message := newRecordingDeviceFactMsg(observationFactMessage(t, validator, defaultObservationFactInput()))
	handleDeviceFactMessage(t.Context(), message, validator, receiver, discardLogger())
	acks, terms, delays := message.dispositions()
	if acks != 0 || terms != 0 || len(delays) != 1 || delays[0] != DeviceFactConsumerNakDelay {
		t.Fatalf("stored corruption disposition: acks=%d terms=%d delays=%v", acks, terms, delays)
	}
}
