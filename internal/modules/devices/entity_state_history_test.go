package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestListEntityStateHistoryValidatesBeforeRepositoryCalls(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	service := newTestService(repository, nil, nil, Dependencies{})
	invalidEntityID := EntityID("bad")
	beforeZero := int64(0)

	for _, test := range []struct {
		name   string
		params ListEntityStateHistoryParams
	}{
		{"invalid entity", ListEntityStateHistoryParams{EntityID: invalidEntityID, Limit: 50}},
		{"limit zero", ListEntityStateHistoryParams{EntityID: commandTestEntityID, Limit: 0}},
		{"limit over maximum", ListEntityStateHistoryParams{EntityID: commandTestEntityID, Limit: 201}},
		{
			"unknown filter",
			ListEntityStateHistoryParams{EntityID: commandTestEntityID, Filter: "recent", Limit: 50},
		},
		{
			"cursor below one",
			ListEntityStateHistoryParams{
				EntityID: commandTestEntityID, Limit: 50, BeforeReceiveOrder: &beforeZero,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := service.ListEntityStateHistory(context.Background(), test.params); !errors.Is(
				err,
				ErrInvalidPage,
			) {
				t.Fatalf("error = %v, want ErrInvalidPage", err)
			}
		})
	}
	if repository.getEntityCalls != 0 || repository.historyCalls != 0 {
		t.Fatalf("repository calls = entity %d, history %d", repository.getEntityCalls, repository.historyCalls)
	}
}

func TestListEntityStateHistoryDefaultsFilterAndDistinguishesUnknownParent(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	repository.entity = EntityWithState{Entity: Entity{ID: commandTestEntityID}}
	repository.historyPage = Page[EntityStateHistoryEntry]{Items: []EntityStateHistoryEntry{}, HasMore: false}
	service := newTestService(repository, nil, nil, Dependencies{})

	page, err := service.ListEntityStateHistory(
		context.Background(),
		ListEntityStateHistoryParams{EntityID: commandTestEntityID, Limit: 50},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("empty history page = %#v", page)
	}
	if repository.getEntityCalls != 1 || repository.historyCalls != 1 ||
		repository.historyParams.EntityID != commandTestEntityID ||
		repository.historyParams.Filter != EntityStateHistoryFilterUpdates ||
		repository.historyParams.Limit != 50 || repository.historyParams.BeforeReceiveOrder != nil {
		t.Fatalf("history params = %#v, entity calls = %d", repository.historyParams, repository.getEntityCalls)
	}

	repository.entityErr = ErrEntityNotFound
	before := int64(7)
	_, err = service.ListEntityStateHistory(context.Background(), ListEntityStateHistoryParams{
		EntityID: commandTestEntityID, Filter: EntityStateHistoryFilterAll,
		BeforeReceiveOrder: &before, Limit: 10,
	})
	if !errors.Is(err, ErrEntityNotFound) || repository.historyCalls != 1 {
		t.Fatalf("error = %v, history calls = %d", err, repository.historyCalls)
	}
}

func TestListEntityStateHistoryReturnsOwnedValues(t *testing.T) {
	t.Parallel()
	sourceUpdatedAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	rejection := RejectionInvalidValue
	repository := newReadRepository()
	repository.entity = EntityWithState{Entity: Entity{ID: commandTestEntityID}}
	repository.historyPage = Page[EntityStateHistoryEntry]{
		Items: []EntityStateHistoryEntry{{
			ObservationID: "obs_01890f47-7a6b-7c4d-8e9f-0123456789a1", Value: Value(`true`),
			Disposition: DispositionApplied, Rejection: nil,
			AdapterReceivedAt: sourceUpdatedAt, SourceUpdatedAt: &sourceUpdatedAt,
			ObservedAt: sourceUpdatedAt, ReceiveOrder: 3,
		}},
		HasMore: true,
	}
	service := newTestService(repository, nil, nil, Dependencies{})

	page, err := service.ListEntityStateHistory(context.Background(), ListEntityStateHistoryParams{
		EntityID: commandTestEntityID, Filter: EntityStateHistoryFilterRejected, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || repository.historyParams.Filter != EntityStateHistoryFilterRejected {
		t.Fatalf("history page = %#v", page)
	}
	page.Items[0].Value[0] = 'f'
	*page.Items[0].SourceUpdatedAt = time.Time{}
	page.Items[0].Rejection = &rejection
	if string(repository.historyPage.Items[0].Value) != "true" ||
		repository.historyPage.Items[0].Rejection != nil ||
		!repository.historyPage.Items[0].SourceUpdatedAt.Equal(sourceUpdatedAt) {
		t.Fatal("ListEntityStateHistory result aliases repository data")
	}
}
