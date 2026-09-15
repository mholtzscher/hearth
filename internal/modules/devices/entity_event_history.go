package devices

import (
	"context"
	"fmt"
	"time"
)

// EntityEventHistoryEntry is one retained first-seen Entity Event, newest
// first by Core receive order.
type EntityEventHistoryEntry struct {
	EventID      EntityEventID
	EntityID     EntityID
	Name         EntityEventName
	Disposition  EntityEventDisposition
	Rejection    *EntityEventRejection
	EmittedAt    time.Time
	ReceivedAt   time.Time
	RecordedAt   time.Time
	ReceiveOrder int64 // internal/cursor position only
}

// ListEntityEventsParams defines one keyset page of an Entity's retained
// Entity Event history. BeforeReceiveOrder is exclusive: a page continues with
// records strictly older than it.
type ListEntityEventsParams struct {
	EntityID           EntityID
	BeforeReceiveOrder *int64
	Limit              int
}

// ListEntityEvents returns one keyset page of an Entity's retained Entity
// Event history. It validates the parent Entity and pagination before reading,
// so an unknown parent is distinguishable from an existing Entity with no
// events and no invalid page reaches persistence.
func (service *Service) ListEntityEvents(
	ctx context.Context,
	params ListEntityEventsParams,
) (Page[EntityEventHistoryEntry], error) {
	if _, err := ParseEntityID(string(params.EntityID)); err != nil {
		return Page[EntityEventHistoryEntry]{}, fmt.Errorf("%w: parse entity ID: %w", ErrInvalidPage, err)
	}
	if !ValidPageLimit(params.Limit) {
		return Page[EntityEventHistoryEntry]{}, ErrInvalidPage
	}
	if params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1 {
		return Page[EntityEventHistoryEntry]{}, fmt.Errorf(
			"%w: Entity Event history position must be at least 1", ErrInvalidPage,
		)
	}
	if _, err := service.stores.Reads.GetEntity(ctx, params.EntityID); err != nil {
		return Page[EntityEventHistoryEntry]{}, err
	}
	page, err := service.stores.EntityEvents.ListEntityEvents(ctx, params)
	if err != nil {
		return Page[EntityEventHistoryEntry]{}, err
	}
	items := make([]EntityEventHistoryEntry, len(page.Items))
	for index, entry := range page.Items {
		items[index] = copyEntityEventHistoryEntry(entry)
	}
	return Page[EntityEventHistoryEntry]{Items: items, HasMore: page.HasMore}, nil
}

func copyEntityEventHistoryEntry(entry EntityEventHistoryEntry) EntityEventHistoryEntry {
	cloned := entry
	if entry.Rejection != nil {
		rejection := *entry.Rejection
		cloned.Rejection = &rejection
	}
	return cloned
}
