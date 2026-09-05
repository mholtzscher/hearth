package devices

import (
	"context"
	"fmt"
	"time"
)

// EntityStateHistoryEntry is one retained first-seen Observation for an Entity,
// newest-first by ReceiveOrder.
type EntityStateHistoryEntry struct {
	ObservationID     ObservationID
	Value             Value // Nil only when Disposition is rejected.
	Disposition       ObservationDisposition
	Rejection         *ObservationRejection
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
	ObservedAt        time.Time
	ReceiveOrder      int64
}

// EntityStateHistoryFilter selects retained Observation dispositions for one Entity.
type EntityStateHistoryFilter string

const (
	EntityStateHistoryFilterUpdates   EntityStateHistoryFilter = "state-updates"
	EntityStateHistoryFilterAll       EntityStateHistoryFilter = "all"
	EntityStateHistoryFilterApplied   EntityStateHistoryFilter = "applied"
	EntityStateHistoryFilterUnchanged EntityStateHistoryFilter = "unchanged"
	EntityStateHistoryFilterRejected  EntityStateHistoryFilter = "rejected"
)

// ListEntityStateHistoryParams defines one keyset page of an Entity's retained State history.
type ListEntityStateHistoryParams struct {
	EntityID           EntityID
	Filter             EntityStateHistoryFilter
	BeforeReceiveOrder *int64
	Limit              int
}

func (service *Service) ListEntityStateHistory(
	ctx context.Context,
	params ListEntityStateHistoryParams,
) (Page[EntityStateHistoryEntry], error) {
	if _, err := ParseEntityID(string(params.EntityID)); err != nil {
		return Page[EntityStateHistoryEntry]{}, fmt.Errorf("%w: parse entity ID: %w", ErrInvalidPage, err)
	}
	if !validPageLimit(params.Limit) {
		return Page[EntityStateHistoryEntry]{}, ErrInvalidPage
	}
	filter := params.Filter
	if filter == "" {
		filter = EntityStateHistoryFilterUpdates
	}
	switch filter {
	case EntityStateHistoryFilterUpdates,
		EntityStateHistoryFilterAll,
		EntityStateHistoryFilterApplied,
		EntityStateHistoryFilterUnchanged,
		EntityStateHistoryFilterRejected:
	default:
		return Page[EntityStateHistoryEntry]{}, fmt.Errorf(
			"%w: unknown State history filter %q",
			ErrInvalidPage,
			params.Filter,
		)
	}
	if params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1 {
		return Page[EntityStateHistoryEntry]{}, fmt.Errorf(
			"%w: State history position must be at least 1",
			ErrInvalidPage,
		)
	}
	if _, err := service.stores.Reads.GetEntity(ctx, params.EntityID); err != nil {
		return Page[EntityStateHistoryEntry]{}, err
	}
	page, err := service.stores.Reads.ListEntityStateHistory(ctx, ListEntityStateHistoryParams{
		EntityID: params.EntityID, Filter: filter,
		BeforeReceiveOrder: params.BeforeReceiveOrder, Limit: params.Limit,
	})
	if err != nil {
		return Page[EntityStateHistoryEntry]{}, err
	}
	items := make([]EntityStateHistoryEntry, len(page.Items))
	for index, entry := range page.Items {
		items[index] = copyEntityStateHistoryEntry(entry)
	}
	return Page[EntityStateHistoryEntry]{Items: items, HasMore: page.HasMore}, nil
}

func copyEntityStateHistoryEntry(entry EntityStateHistoryEntry) EntityStateHistoryEntry {
	cloned := entry
	cloned.Value = append(Value(nil), entry.Value...)
	if entry.Rejection != nil {
		rejection := *entry.Rejection
		cloned.Rejection = &rejection
	}
	if entry.SourceUpdatedAt != nil {
		sourceUpdatedAt := *entry.SourceUpdatedAt
		cloned.SourceUpdatedAt = &sourceUpdatedAt
	}
	return cloned
}
