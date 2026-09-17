package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

const entityEventAPIAdapter = "simulator"

const entityEventAPIRuntimeID = devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")

// entityEventAPIFixture assembles the real devices Service over real SQLite and
// the real Huma route, so history reads exercise SQL, domain validation, and
// transport mapping together.
type entityEventAPIFixture struct {
	router   http.Handler
	service  *devices.Service
	database *sql.DB
	buttons  devices.EntityID
	power    devices.EntityID
}

func newEntityEventAPIFixture(t *testing.T) entityEventAPIFixture {
	t.Helper()
	ctx := context.Background()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devicessqlite.NewDeviceRepository(database, catalog)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	service := devices.NewService(devicessqlite.DeviceStores(repository), nil, catalog, devices.Dependencies{
		Now: func() time.Time { return now },
	})
	if claimErr := repository.ClaimAdapterRuntime(ctx, devices.ClaimRuntimeWrite{
		RuntimeID: entityEventAPIRuntimeID, AdapterID: entityEventAPIAdapter,
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
		ClaimedAt: now, LeaseExpiresAt: now.Add(time.Hour),
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	externalID := "sim-buttons"
	binding, err := service.Register(ctx, entityEventAPIAdapter, entityEventAPIRuntimeID, devices.Registration{
		BindingKey: "office-buttons",
		Device: devices.DeviceDescriptor{
			ExternalID: &externalID, Name: "Office buttons", Kind: devices.DeviceKindSensor,
		},
		Entities: []devices.EntityDescriptor{
			{
				Key: "buttons", ExternalID: "sim.buttons", Name: "Buttons",
				TypeID: devices.EntityTypeEnumeventV1,
				Support: devices.EntitySupport(
					`{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}`,
				),
			},
			{
				Key: "power", ExternalID: "sim.power", Name: "Power",
				TypeID:  devices.EntityTypePowerV1,
				Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router, _ := testAPI(t, service)
	return entityEventAPIFixture{
		router: router, service: service, database: database,
		buttons: binding.Entities[0].EntityID, power: binding.Entities[1].EntityID,
	}
}

// record publishes one report for the event-source Entity with the client-owned
// minted identity the wire path would produce.
func (fixture entityEventAPIFixture) record(t *testing.T, name string, emittedAt time.Time) {
	t.Helper()
	eventID, err := devices.NewEntityEventID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, correlationErr := devices.NewCorrelationID()
	if correlationErr != nil {
		t.Fatal(correlationErr)
	}
	if _, recordErr := fixture.service.RecordEntityEvent(
		context.Background(), entityEventAPIAdapter, entityEventAPIRuntimeID, devices.EntityEvent{
			ID: eventID, EntityID: fixture.buttons, Name: devices.EntityEventName(name),
			CorrelationID: correlationID, EmittedAt: emittedAt,
		}, emittedAt.Add(time.Minute),
	); recordErr != nil {
		t.Fatal(recordErr)
	}
}

func countEntityEventAPIRows(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// This test protects A7 ordering, disposition, timestamp, cursor, and privacy
// behavior through real SQLite and the Huma route. It fails if page order
// follows emitted_at instead of Core receive order, if a rejection code or
// timestamp drifts, or if an internal identifier leaks.
func TestEntityEventHistoryThroughRealSQLiteAndHumaRoute(t *testing.T) {
	t.Parallel()
	fixture := newEntityEventAPIFixture(t)
	// Receive order is A, B, C. C claims the oldest emitted_at, so page order
	// must disagree with emitted_at order and follow Core receive order.
	fixture.record(t, "single_press", time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))
	fixture.record(t, "triple_press", time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)) // Unsupported: rejected.
	fixture.record(t, "double_press", time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC))

	response := performRequest(fixture.router, "/v1/entities/"+string(fixture.buttons)+"/events?limit=2")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var first EntityEventCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	newest, rejected := first.Items[0], first.Items[1]
	if newest.Name != "double_press" || newest.Disposition != "accepted" || newest.RejectionCode != nil {
		t.Fatalf("newest entry = %#v", newest)
	}
	if newest.EmittedAt != "2026-09-10T08:00:00Z" || newest.ReceivedAt != "2026-09-10T08:01:00Z" ||
		newest.RecordedAt != "2026-09-10T12:00:00Z" {
		t.Fatalf("newest entry timestamps = %#v", newest)
	}
	if rejected.Name != "triple_press" || rejected.Disposition != "rejected" ||
		rejected.RejectionCode == nil || *rejected.RejectionCode != "unsupported_event" ||
		rejected.EntityID != string(fixture.buttons) {
		t.Fatalf("rejected entry = %#v", rejected)
	}
	for _, field := range []string{
		"adapter_id", "runtime_id", "correlation_id", "fingerprint", "receive_order",
	} {
		if containsJSONField(response.Body.Bytes(), field) {
			t.Fatalf("%s leaked into %s", field, response.Body.String())
		}
	}

	response = performRequest(
		fixture.router,
		"/v1/entities/"+string(fixture.buttons)+"/events?limit=2&cursor="+*first.NextCursor,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", response.Code, response.Body.String())
	}
	var second EntityEventCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].Name != "single_press" {
		t.Fatalf("second page = %#v", second)
	}
}

// This test protects the parent contract and non-execution through real SQLite
// and the Huma route. It fails if an existing Entity with no events returns 404,
// if an unknown parent returns an empty page, or if reading history writes
// State, Observations, Commands, or Entity Events.
func TestEntityEventHistoryDistinguishesUnknownAndEmptyParents(t *testing.T) {
	t.Parallel()
	fixture := newEntityEventAPIFixture(t)
	fixture.record(t, "single_press", time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))

	// An existing non-event Entity has no events and is not a 404.
	response := performRequest(fixture.router, "/v1/entities/"+string(fixture.power)+"/events")
	if response.Code != http.StatusOK {
		t.Fatalf("empty status = %d, body = %s", response.Code, response.Body.String())
	}
	var empty map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if items, ok := empty["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("empty page = %s", response.Body.String())
	}
	if _, hasCursor := empty["next_cursor"]; hasCursor {
		t.Fatalf("empty page exposed next_cursor: %s", response.Body.String())
	}

	unknownID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	response = performRequest(fixture.router, "/v1/entities/"+string(unknownID)+"/events")
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown parent status = %d, body = %s", response.Code, response.Body.String())
	}

	// Reading history executes no work.
	if count := countEntityEventAPIRows(t, fixture.database, "entity_events"); count != 1 {
		t.Fatalf("entity_events count after reads = %d, want 1", count)
	}
	for _, table := range []string{"observations", "entity_states", "commands"} {
		if count := countEntityEventAPIRows(t, fixture.database, table); count != 0 {
			t.Fatalf("%s count after reads = %d, want 0", table, count)
		}
	}
}
