package sqlite //nolint:testpackage // Tests exercise package-private row mapping functions.

import (
	"database/sql"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestFactHistoryMappingDistinguishesAbsentAndJSONNullPredecessors(t *testing.T) {
	t.Parallel()
	base := dbsqlc.AutomationHistory{
		ID:              "arn_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		FactID:          sql.NullString{String: "fct_01890f47-7a6b-7c4d-8e9f-0123456789ab", Valid: true},
		FactFamily:      sql.NullString{String: string(automations.DeviceFactObservation), Valid: true},
		FactEntityID:    sql.NullString{String: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab", Valid: true},
		FactVariant:     sql.NullString{String: string(devices.DispositionApplied), Valid: true},
		FactCausationID: sql.NullString{String: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab", Valid: true},
		FactValueJson:   sql.NullString{String: "false", Valid: true},
		FactEmittedAt:   sql.NullString{String: "2026-09-22T00:00:00.000000000Z", Valid: true},
	}
	for _, test := range []struct {
		name     string
		previous sql.NullString
		want     []byte
	}{
		{name: "absent"},
		{name: "JSON null", previous: sql.NullString{String: "null", Valid: true}, want: []byte("null")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			row := base
			row.FactPreviousValueJson = test.previous
			summary, err := factSummaryFromRow(row)
			if err != nil {
				t.Fatal(err)
			}
			if string(summary.PreviousStateValue) != string(test.want) {
				t.Fatalf("previous state = %q, want %q", summary.PreviousStateValue, test.want)
			}
			stored := storedFactColumns(summary)
			if stored.previousValueJSON.Valid != test.previous.Valid ||
				stored.previousValueJSON.String != test.previous.String {
				t.Fatalf("stored predecessor = %#v, want %#v", stored.previousValueJSON, test.previous)
			}
		})
	}
}
