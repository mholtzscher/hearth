package sqlite //nolint:testpackage // Tests exercise the stored-row decoder directly.

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func TestCommandDecoderRejectsMalformedReferencedIdentities(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, entity, runtime string }{
		{"entity", "ent_private_stored_value", "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"},
		{"runtime", "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab", "run_private_stored_value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := commandFromRow(dbsqlc.Command{
				ID:            "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				EntityID:      test.entity,
				RuntimeID:     sql.NullString{String: test.runtime, Valid: true},
				RequestedAt:   "2026-10-29T12:00:00.000000000Z",
				DeadlineAt:    "2026-10-29T12:00:10.000000000Z",
			})
			if _, ok := errors.AsType[*storedDataError](err); !ok {
				t.Fatalf("want private stored-data failure, got %v", err)
			}
		})
	}
}
