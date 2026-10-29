package automations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestScheduleConfigurationPreconditionsAreInternal(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"activation location", "activation time", "processing location"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			dependencies := automations.Dependencies{}
			at := time.Now().UTC()
			if missing == "activation time" {
				at = time.Time{}
				dependencies.HouseholdLocation = time.UTC
			}
			service := automations.NewService(nil, &stubAutomationDevices{}, dependencies)
			var err error
			if missing == "processing location" {
				_, err = service.ProcessDueSchedules(t.Context())
			} else {
				err = service.InitializeSchedules(t.Context(), at)
			}
			if err == nil || errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("schedule precondition error=%v, want internal failure", err)
			}
		})
	}
}
