package devices //nolint:testpackage // Tests exercise availability history ownership.
import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceAvailabilityHistoryReturnsOwnedCopiesAndValidatesPages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	sourceObservedAt := now.Add(-time.Second)
	repository := &availabilityHistoryRepositoryStub{}
	repository.availabilityHistoryPage = Page[HealthTransition]{Items: []HealthTransition{{
		ReceiveOrder: 5, Status: string(EntityAvailabilityUnavailable), Source: "adapter_health",
		Reason:           &HealthReason{Code: "hearth.network_unreachable"},
		SourceObservedAt: &sourceObservedAt, ObservedAt: now,
	}}, HasMore: true}
	service := newTestService(repository, nil, nil, Dependencies{})
	availabilityHistory, err := service.ListEntityAvailabilityHistory(
		ctx,
		ListEntityAvailabilityParams{EntityID: commandTestEntityID, Limit: 10},
	)
	if err != nil || !availabilityHistory.HasMore || len(availabilityHistory.Items) != 1 {
		t.Fatalf("availability history = %#v, %v", availabilityHistory, err)
	}
	availabilityHistory.Items[0].Reason.Code = "changed"
	if repository.availabilityHistoryPage.Items[0].Reason.Code != "hearth.network_unreachable" {
		t.Fatal("availability history result aliases repository data")
	}
	if _, invalidErr := service.ListEntityAvailabilityHistory(ctx, ListEntityAvailabilityParams{
		EntityID: "invalid", Limit: 10,
	}); !errors.Is(invalidErr, ErrInvalidPage) || repository.availabilityHistoryCalls != 1 {
		t.Fatalf("invalid availability history error = %v, calls = %d", invalidErr, repository.availabilityHistoryCalls)
	}
}
