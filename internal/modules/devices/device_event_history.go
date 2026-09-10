package devices

import (
	"context"
	"fmt"
	"time"
)

// DeviceEventHistoryEntry is one retained first-seen Device Event, newest
// first by Core receive order.
type DeviceEventHistoryEntry struct {
	EventID      DeviceEventID
	EntityID     EntityID
	Name         DeviceEventName
	Disposition  DeviceEventDisposition
	Rejection    *DeviceEventRejection
	EmittedAt    time.Time
	ReceivedAt   time.Time
	RecordedAt   time.Time
	ReceiveOrder int64 // internal/cursor position only
}

// ListEntityDeviceEventsParams defines one keyset page of an Entity's retained
// Device Event history. BeforeReceiveOrder is exclusive: a page continues with
// records strictly older than it.
type ListEntityDeviceEventsParams struct {
	EntityID           EntityID
	BeforeReceiveOrder *int64
	Limit              int
}

// ListEntityDeviceEvents returns one keyset page of an Entity's retained Device
// Event history. It validates the parent Entity and pagination before reading,
// so an unknown parent is distinguishable from an existing Entity with no
// events and no invalid page reaches persistence.
func (service *Service) ListEntityDeviceEvents(
	ctx context.Context,
	params ListEntityDeviceEventsParams,
) (Page[DeviceEventHistoryEntry], error) {
	if _, err := ParseEntityID(string(params.EntityID)); err != nil {
		return Page[DeviceEventHistoryEntry]{}, fmt.Errorf("%w: parse entity ID: %w", ErrInvalidPage, err)
	}
	if !validPageLimit(params.Limit) {
		return Page[DeviceEventHistoryEntry]{}, ErrInvalidPage
	}
	if params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1 {
		return Page[DeviceEventHistoryEntry]{}, fmt.Errorf(
			"%w: Device Event history position must be at least 1", ErrInvalidPage,
		)
	}
	if _, err := service.stores.Reads.GetEntity(ctx, params.EntityID); err != nil {
		return Page[DeviceEventHistoryEntry]{}, err
	}
	page, err := service.stores.DeviceEvents.ListEntityDeviceEvents(ctx, params)
	if err != nil {
		return Page[DeviceEventHistoryEntry]{}, err
	}
	items := make([]DeviceEventHistoryEntry, len(page.Items))
	for index, entry := range page.Items {
		items[index] = copyDeviceEventHistoryEntry(entry)
	}
	return Page[DeviceEventHistoryEntry]{Items: items, HasMore: page.HasMore}, nil
}

func copyDeviceEventHistoryEntry(entry DeviceEventHistoryEntry) DeviceEventHistoryEntry {
	cloned := entry
	if entry.Rejection != nil {
		rejection := *entry.Rejection
		cloned.Rejection = &rejection
	}
	return cloned
}
