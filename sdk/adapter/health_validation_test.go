package adapter //nolint:testpackage // Tests exercise private health report validation.

import (
	"testing"
	"time"
)

func TestHealthValidationAcceptsDocumentedAdapterNamespace(t *testing.T) {
	t.Parallel()
	report := HealthReport{
		Status: HealthUnhealthy, SourceObservedAt: time.Now().UTC(),
		ReasonCode: "adapter.entity_unavailable",
	}
	if err := validateHealthReport(report); err != nil {
		t.Fatalf("documented Adapter reason rejected: %v", err)
	}
	report.ReasonCode = "vendor.offline"
	if err := validateHealthReport(report); err == nil {
		t.Fatal("unsupported reason namespace was accepted")
	}
}
