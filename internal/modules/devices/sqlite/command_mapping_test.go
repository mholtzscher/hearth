package sqlite //nolint:testpackage // Tests exercise the stored-row decoder directly.

import (
	"database/sql"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func TestCommandDecoderPreservesRetainedIdentityText(t *testing.T) {
	t.Parallel()
	const retainedUUID = "00000000-0000-0000-0000-000000000000"
	row := dbsqlc.Command{
		ID: "cmd_" + retainedUUID, CorrelationID: "cor_" + retainedUUID, EntityID: "ent_" + retainedUUID,
		RuntimeID:            sql.NullString{String: "run_" + retainedUUID, Valid: true},
		OutcomeObservationID: sql.NullString{String: "obs_" + retainedUUID, Valid: true},
		Status:               "satisfied", AdapterID: "alerts", Operation: "set", ParametersJson: `{}`,
		CompletedAt: sql.NullString{String: "2026-10-29T12:00:01.000000000Z", Valid: true},
		RequestedAt: "2026-10-29T12:00:00.000000000Z", DeadlineAt: "2026-10-29T12:00:10.000000000Z",
	}
	got, err := commandFromRow(row)
	if err != nil || string(got.ID) != row.ID || string(got.CorrelationID) != row.CorrelationID ||
		string(
			got.EntityID,
		) != row.EntityID || got.RuntimeID == nil || string(*got.RuntimeID) != row.RuntimeID.String ||
		got.OutcomeObservationID == nil || string(*got.OutcomeObservationID) != row.OutcomeObservationID.String {
		t.Fatalf("decoder reinterpreted retained identities: %+v, %v", got, err)
	}
}
