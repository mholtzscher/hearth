//go:build integration

package sqlite //nolint:testpackage // Tests exercise the transactional registration boundary.

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gocognit // Each row drives registration, mutable support, permanent rejection, and the retained baseline.
func TestRegistrationFreezesOnlyInterpretationReferences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                          string
		id                            devices.EntityTypeID
		first, same, mutable, changed string
	}{
		{"scale", devices.EntityTypeBrightnessV1,
			`{"state":{"maximum":99},"operations":{"set":{"step":1}}}`,
			`{"state":{"maximum":99.0},"operations":{"set":{"step":1}}}`,
			`{"state":{"maximum":99},"operations":{"set":{"step":3}}}`,
			`{"state":{"maximum":100},"operations":{"set":{"step":1}}}`},
		{"unit", devices.EntityTypeNumericsensorV1,
			`{"state":{"minimum":0,"maximum":100,"unit":"lqi"},"operations":{}}`,
			`{"state":{"minimum":0,"maximum":100,"unit":"lqi"},"operations":{}}`,
			`{"state":{"minimum":-1,"maximum":200,"unit":"lqi"},"operations":{}}`,
			`{"state":{"minimum":0,"maximum":100,"unit":"V"},"operations":{}}`},
		{"optional absent", devices.EntityTypeNumericsettingV1,
			`{"state":{"minimum":0,"maximum":100,"choices":["previous"]},"operations":{"set":{}}}`,
			`{"state":{"minimum":0,"maximum":100,"choices":["previous"]},"operations":{"set":{}}}`,
			`{"state":{"minimum":-1,"maximum":200,"choices":["off"]},"operations":{"set":{}}}`,
			`{"state":{"minimum":0,"maximum":100,"choices":["previous"],"unit":"1"},"operations":{"set":{}}}`},
		{"optional present", devices.EntityTypeNumericsettingV1,
			`{"state":{"minimum":0,"maximum":100,"choices":["previous"],"unit":"1"},"operations":{"set":{}}}`,
			`{"state":{"minimum":0,"maximum":100,"choices":["previous"],"unit":"1"},"operations":{"set":{}}}`,
			`{"state":{"minimum":-1,"maximum":200,"choices":["off"],"unit":"1"},"operations":{"set":{}}}`,
			`{"state":{"minimum":0,"maximum":100,"choices":["previous"]},"operations":{"set":{}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
			catalog := firstLightCatalog(t)
			service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
			registration := validDomainRegistration()
			registration.Entities[0].TypeID = test.id
			registration.Entities[0].Support = devices.EntitySupport(test.first)
			first, err := service.Register(ctx, "simulator", testRuntimeID, registration)
			if err != nil {
				t.Fatal(err)
			}
			for _, support := range []string{test.same, test.mutable} {
				registration.Entities[0].Support = devices.EntitySupport(support)
				next, registerErr := service.Register(ctx, "simulator", testRuntimeID, registration)
				if registerErr != nil || !reflect.DeepEqual(first, next) {
					t.Fatalf("re-register = %+v, %v", next, registerErr)
				}
			}
			registration.Entities[0].Support = devices.EntitySupport(test.changed)
			_, err = service.Register(ctx, "simulator", testRuntimeID, registration)
			assertRegistrationRejection(t, err, devices.RegistrationImmutableInterpretationChange)
			view, err := service.GetEntity(ctx, first.Entities[0].EntityID)
			if err != nil {
				t.Fatal(err)
			}
			normalized, err := catalog.NormalizeSupport(test.id, devices.EntitySupport(test.mutable))
			if err != nil {
				t.Fatal(err)
			}
			if string(view.Entity.Support) != string(normalized) {
				t.Fatalf("rejected support replaced baseline: %s", view.Entity.Support)
			}
		})
	}
}

func TestInterpretationRejectionRollsBackEntireBatchAndRetainsEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	registration := multiEntityRegistration()
	registration.Entities[1].Support = devices.EntitySupport(`{"state":{"maximum":99},"operations":{"set":{"step":1}}}`)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	observation := newObservation(t, binding.Entities[1].EntityID, `50`, at)
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, observation, at)
	if err != nil || result.Disposition != devices.DispositionApplied {
		t.Fatalf("observation = %+v, %v", result, err)
	}
	command := newCommandRecord(t, binding.Entities[1].EntityID, at)
	command.Parameters = devices.CommandParameters(`{"value":70}`)
	if _, createErr := repository.CreateCommand(ctx, command); createErr != nil {
		t.Fatal(createErr)
	}
	before := registrationSnapshot(t, database)
	registration.Device.Name = "must roll back"
	external := "renamed-device"
	registration.Device.ExternalID = &external
	registration.Entities[0].Name = "must roll back"
	registration.Entities[0].ExternalID = "must-roll-back"
	registration.Entities[1].Support = devices.EntitySupport(
		`{"state":{"maximum":100},"operations":{"set":{"step":1}}}`,
	)
	registration.Entities = append(
		registration.Entities,
		devices.EntityDescriptor{
			Key:        "temperature",
			ExternalID: "new.temperature",
			Name:       "Must not exist",
			TypeID:     devices.EntityTypeTemperatureV1,
			Support:    devices.EntitySupport(`{"state":{},"operations":{}}`),
		},
	)
	_, err = service.Register(ctx, "simulator", testRuntimeID, registration)
	assertRegistrationRejection(t, err, devices.RegistrationImmutableInterpretationChange)
	if after := registrationSnapshot(t, database); !reflect.DeepEqual(before, after) {
		t.Fatalf(
			"registration rejection changed persisted descriptors or evidence:\nbefore=%v\nafter=%v",
			before,
			after,
		)
	}
	// The old scale still enforces native State and exact Command satisfaction.
	entity, err := service.GetEntity(ctx, binding.Entities[1].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, stateErr := catalog.NormalizeState(entity.Entity, devices.Value(`100`)); stateErr == nil {
		t.Fatal("State above immutable native maximum accepted")
	}
}

func registrationSnapshot(t *testing.T, database *sql.DB) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	for _, table := range []string{"devices", "entities", "adapter_bindings", "adapter_entity_mappings", "entity_states", "observations", "commands"} {
		snapshot[table] = registrationTableSnapshot(t, database, table)
	}
	return snapshot
}

func registrationTableSnapshot(t *testing.T, database *sql.DB, table string) string {
	t.Helper()
	//nolint:unqueryvet // Snapshot every persisted column; schema additions must be covered by rollback too.
	rows, err := database.QueryContext(context.Background(), "SELECT * FROM "+table+" ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var records [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for index := range values {
			dest[index] = &values[index]
		}
		if scanErr := rows.Scan(dest...); scanErr != nil {
			t.Fatal(scanErr)
		}
		records = append(records, values)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
