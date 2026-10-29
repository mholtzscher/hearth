package sqlite //nolint:testpackage // Tests exercise the selected history row decoders.

import (
	"database/sql"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestHistoryDecodersPreserveRetainedIdentityText(t *testing.T) {
	t.Parallel()
	const timestamp = "2026-10-29T12:00:00.000000000Z"
	const retainedUUID = "00000000-0000-0000-0000-000000000000"
	event, err := entityEventHistoryEntryFromRow(entityEventHistoryRow{
		EventID: "evt_" + retainedUUID, EntityID: "ent_" + retainedUUID, Name: "single_press",
		Disposition: "accepted", EmittedAt: timestamp, ReceivedAt: timestamp, RecordedAt: timestamp,
	})
	if err != nil || event.EventID != "evt_"+retainedUUID || event.EntityID != "ent_"+retainedUUID {
		t.Fatalf("event decoder reinterpreted retained identities: %+v, %v", event, err)
	}
	state, err := entityStateHistoryEntryFromRow(entityStateHistoryRow{
		ObservationID:     "obs_retained",
		Disposition:       "applied",
		StateValueJSON:    sql.NullString{String: "true", Valid: true},
		AdapterReceivedAt: timestamp,
		ObservedAt:        timestamp,
	})
	if err != nil || state.ObservationID != "obs_retained" || state.Disposition != devices.DispositionApplied {
		t.Fatalf("state decoder reinterpreted retained identity: %+v, %v", state, err)
	}
}
