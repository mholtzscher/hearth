package devices //nolint:testpackage // Tests exercise package-private lease-expiry state.

import (
	"context"
	"testing"
	"time"
)

type availabilityServiceRepository struct {
	write AvailabilityBatchWrite
	calls int
}

func (repository *availabilityServiceRepository) ReportEntityAvailability(
	_ context.Context,
	write AvailabilityBatchWrite,
) (time.Time, error) {
	repository.calls++
	repository.write = write
	return write.ReportedAt, nil
}

func TestReportEntityAvailabilityAllowsReadinessPauseAndOwnsInput(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	entityID, err := ParseEntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	repository := &availabilityServiceRepository{}
	service := newTestService(repository, nil, nil, Dependencies{Now: func() time.Time { return now }})
	reports := []EntityAvailabilityReport{{
		EntityID: entityID, Status: EntityAvailabilityUnavailable,
		SourceObservedAt: now.In(time.FixedZone("offset", 2*60*60)),
		Reason:           &HealthReason{Code: "adapter.some-other-software.entity_unavailable"},
	}}

	reportedAt, err := service.ReportEntityAvailability(
		context.Background(), "simulator", testRuntimeID, reports,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reportedAt.Equal(now) || repository.calls != 1 || repository.write.AdapterID != "simulator" ||
		repository.write.RuntimeID != testRuntimeID || !repository.write.ReportedAt.Equal(now) ||
		len(repository.write.Reports) != 1 || repository.write.Reports[0].SourceObservedAt.Location() != time.UTC {
		t.Fatalf("availability write = %#v, reported at %v", repository.write, reportedAt)
	}

	reports[0].Reason.Code = "hearth.changed"
	if repository.write.Reports[0].Reason.Code != "adapter.some-other-software.entity_unavailable" {
		t.Fatal("repository availability write aliases caller input")
	}
}

func TestReportEntityAvailabilityRejectsMalformedBatchesBeforePersistence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	entityID, err := ParseEntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	repository := &availabilityServiceRepository{}
	service := newTestService(repository, nil, nil, Dependencies{Now: func() time.Time { return now }})
	valid := EntityAvailabilityReport{
		EntityID: entityID, Status: EntityAvailabilityAvailable, SourceObservedAt: now,
	}
	for _, test := range []struct {
		name    string
		reports []EntityAvailabilityReport
	}{
		{name: "empty"},
		{name: "duplicate Entity", reports: []EntityAvailabilityReport{valid, valid}},
		{name: "unknown status", reports: []EntityAvailabilityReport{{
			EntityID: entityID, Status: EntityAvailabilityUnknown, SourceObservedAt: now,
		}}},
		{name: "available reason", reports: []EntityAvailabilityReport{{
			EntityID: entityID, Status: EntityAvailabilityAvailable, SourceObservedAt: now,
			Reason: &HealthReason{Code: "hearth.entity_unavailable"},
		}}},
		{name: "unavailable without reason", reports: []EntityAvailabilityReport{{
			EntityID: entityID, Status: EntityAvailabilityUnavailable, SourceObservedAt: now,
		}}},
		{name: "zero source time", reports: []EntityAvailabilityReport{{
			EntityID: entityID, Status: EntityAvailabilityAvailable,
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, reportErr := service.ReportEntityAvailability(
				context.Background(), "simulator", testRuntimeID, test.reports,
			); reportErr == nil {
				t.Fatal("malformed availability batch was accepted")
			}
		})
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d, want 0", repository.calls)
	}
}
