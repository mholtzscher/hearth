//go:build integration

package nats //nolint:testpackage // Exercises real admission through the private message disposition boundary.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func TestDeviceFactServerTimesDoNotTerminateValidFacts(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"admission time", "startup time"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "preconditions.db"))
			repository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
			validTime := mustTestTime(testEmittedAtString)
			first := true
			dependencies := automations.Dependencies{Now: func() time.Time {
				if missing == "admission time" || first {
					first = false
					return time.Time{}
				}
				return validTime
			}}
			if missing == "admission time" {
				dependencies.HeldStateStartupAt = validTime
			}
			service := automations.NewService(repository, nil, dependencies)
			validator := testValidator(t)
			message := newRecordingDeviceFactMsg(observationFactMessage(t, validator, defaultObservationFactInput()))
			handleDeviceFactMessage(t.Context(), message, validator, service, discardLogger())
			acks, terms, delays := message.dispositions()
			if acks != 0 || terms != 0 || len(delays) != 1 || delays[0] != DeviceFactConsumerNakDelay {
				t.Errorf("server time failure: ack=%d term=%d delays=%v, want delayed Nak", acks, terms, delays)
			}
			var count int
			if err := database.QueryRowContext(t.Context(), "SELECT count(*) FROM automation_fact_receipts").
				Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("failed admission wrote %d Fact receipts", count)
			}
		})
	}
}
