package devices //nolint:testpackage // Tests exercise package-private health evaluation state.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type availabilityServiceRepository struct {
	*stubRegistrationRepository

	adapter AdapterInstance
	write   AvailabilityBatchWrite
	calls   int
}

func (repository *availabilityServiceRepository) ReportEntityAvailability(
	_ context.Context,
	write AvailabilityBatchWrite,
) (time.Time, error) {
	repository.calls++
	repository.write = write
	return write.ReportedAt, nil
}

func (repository *availabilityServiceRepository) GetAdapter(
	context.Context,
	string,
) (AdapterInstance, error) {
	return copyAdapterInstance(repository.adapter), nil
}

func TestReportEntityAvailabilityValidatesEvaluationAndOwnsInput(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	entityID, err := ParseEntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	repository := &availabilityServiceRepository{
		stubRegistrationRepository: &stubRegistrationRepository{},
		adapter: AdapterInstance{ID: "simulator", Health: &AdapterHealth{
			Runtime: &RuntimeEvidence{
				ID: testRuntimeID, Status: runtimeStatusOnline, SoftwareName: "hearth-simulator",
			},
		}},
	}
	service := NewService(repository, nil, nil, Dependencies{Now: func() time.Time { return now }})
	detail := "upstream resource missing"
	reports := []EntityAvailabilityReport{{
		EntityID: entityID, Status: EntityAvailabilityUnavailable,
		SourceObservedAt: now.In(time.FixedZone("offset", 2*60*60)),
		Reason: &HealthReason{
			Code: "adapter.hearth-simulator.entity_unavailable", Detail: &detail,
		},
	}}

	if _, reportErr := service.ReportEntityAvailability(
		context.Background(), "simulator", testRuntimeID, reports,
	); !errors.Is(reportErr, ErrHealthEvaluationPaused) {
		t.Fatalf("paused report error = %v", reportErr)
	}
	service.ResumeHealthEvaluation(now.Add(-time.Second))
	if _, reportErr := service.ReportEntityAvailability(
		context.Background(), "simulator", testRuntimeID, reports,
	); !errors.Is(reportErr, ErrAdapterUnhealthy) {
		t.Fatalf("unrefreshed recovery report error = %v", reportErr)
	}
	service.markRecoveryHeartbeat(1, testRuntimeID)
	reportedAt, err := service.ReportEntityAvailability(
		context.Background(), "simulator", testRuntimeID, reports,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reportedAt.Equal(now) || repository.calls != 1 || repository.write.AdapterID != "simulator" ||
		repository.write.RuntimeID != testRuntimeID || !repository.write.ReportedAt.Equal(now) ||
		len(repository.write.Reports) != 1 ||
		repository.write.Reports[0].SourceObservedAt.Location() != time.UTC {
		t.Fatalf("availability write = %#v, reported at %v", repository.write, reportedAt)
	}

	reports[0].Reason.Code = "hearth.changed"
	detail = "changed"
	if repository.write.Reports[0].Reason.Code != "adapter.hearth-simulator.entity_unavailable" ||
		*repository.write.Reports[0].Reason.Detail != "upstream resource missing" {
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
	repository := &availabilityServiceRepository{stubRegistrationRepository: &stubRegistrationRepository{}}
	service := NewService(repository, nil, nil, Dependencies{Now: func() time.Time { return now }})
	service.ResumeHealthEvaluation(now.Add(-adapterLeaseDuration))
	service.markRecoveryHeartbeat(1, testRuntimeID)
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
