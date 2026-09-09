package automations //nolint:testpackage // Unit tests exercise the same predicate used by private history reads.

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestAutomationStepCommandOwnershipRequiresBothIdentities(t *testing.T) {
	t.Parallel()
	command := devices.CommandID("cmd_01900000-0000-7000-8000-000000000001")
	correlation := devices.CorrelationID("cor_01900000-0000-7000-8000-000000000002")
	step := AutomationRunStep{ReservedCommandID: &command, ReservedCorrelationID: &correlation}
	record := devices.CommandRecord{ID: command, CorrelationID: correlation}
	if !AutomationStepOwnsCommand(step, record) {
		t.Fatal("matching independent reservation not owned")
	}
	record.ID = "cmd_01900000-0000-7000-8000-000000000003"
	if AutomationStepOwnsCommand(step, record) {
		t.Fatal("correlation-only match adopted")
	}
	record.ID = command
	record.CorrelationID = "cor_01900000-0000-7000-8000-000000000004"
	if AutomationStepOwnsCommand(step, record) {
		t.Fatal("ID-only match adopted")
	}
	record.CorrelationID = correlation
	step.ReservedCorrelationID = nil
	if AutomationStepOwnsCommand(step, record) {
		t.Fatal("unreserved marker adopted")
	}
}
