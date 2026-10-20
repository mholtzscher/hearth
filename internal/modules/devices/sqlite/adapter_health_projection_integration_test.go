//go:build integration

package sqlite //nolint:testpackage // Real SQLite faults and retained evidence are part of this boundary.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type projectionFixture struct {
	database   *sql.DB
	adapters   *adapters.Service
	repository *DeviceRepository
	service    *devices.Service
	entityID   devices.EntityID
	at         time.Time
}

func newProjectionFixture(t *testing.T) *projectionFixture {
	t.Helper()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "projection.db"))
	catalog := firstLightCatalog(t)
	f := &projectionFixture{
		database:   database,
		repository: NewDeviceRepository(database, catalog),
		at:         time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}
	f.adapters = adapters.NewService(
		adapterssqlite.AdapterStores(adapterssqlite.NewAdapterRepository(database)),
		adapters.Dependencies{Now: func() time.Time { return f.at }},
	)
	stores := DeviceStores(f.repository)
	stores.AdapterReads = f.adapters
	f.service = devices.NewService(stores, nil, catalog, devices.Dependencies{Now: func() time.Time { return f.at }})
	ctx := context.Background()
	if err := f.adapters.ClaimAdapterRuntime(ctx, adapters.ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: testRuntimeID, SoftwareName: "hearth-simulator", SoftwareVersion: "1",
	}); err != nil {
		t.Fatal(err)
	}
	f.at = f.at.Add(time.Second)
	f.heartbeat(t, adapters.AdapterHealthHealthy)
	binding, err := f.service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	f.entityID = binding.Entities[0].EntityID
	return f
}

func (f *projectionFixture) heartbeat(t *testing.T, status adapters.AdapterHealthStatus) {
	t.Helper()
	input := adapters.AdapterHeartbeat{
		AdapterID:        "simulator",
		RuntimeID:        testRuntimeID,
		ExternalStatus:   status,
		SourceObservedAt: f.at,
	}
	if status == adapters.AdapterHealthUnhealthy {
		input.Reason = &adapters.HealthReason{Code: "adapter.simulator.offline"}
	}
	if _, err := f.adapters.RecordAdapterHeartbeat(context.Background(), input); err != nil {
		t.Fatal(err)
	}
}

func (f *projectionFixture) head(t *testing.T) adapters.HealthSnapshot {
	t.Helper()
	var body string
	if err := f.database.QueryRow("SELECT snapshot_json FROM adapter_health_heads WHERE adapter_id = 'simulator'").
		Scan(&body); err != nil {
		t.Fatal(err)
	}
	snapshot, err := adapters.DecodeHealthSnapshot([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func projectionUpdate(t *testing.T, snapshot adapters.HealthSnapshot) devices.AdapterHealthUpdate {
	t.Helper()
	id, err := adapters.NewHealthUpdateID()
	if err != nil {
		t.Fatal(err)
	}
	return devices.AdapterHealthUpdate{MessageID: string(id), Snapshot: snapshot}
}

func (f *projectionFixture) apply(t *testing.T, snapshot adapters.HealthSnapshot) {
	t.Helper()
	update := projectionUpdate(t, snapshot)
	result, err := f.repository.ApplyAdapterHealth(context.Background(), update, f.at)
	if err != nil || result.Disposition != devices.HealthProjectionApplied {
		t.Fatalf("apply = %#v, %v", result, err)
	}
}

func (f *projectionFixture) availability(
	t *testing.T,
	status devices.EntityAvailabilityStatus,
	reason string,
) devices.EntityAvailability {
	t.Helper()
	view, err := f.repository.GetEntity(context.Background(), f.entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != status || healthReasonCode(view.Availability.Reason) != reason {
		t.Fatalf("availability = %#v, want %s / %s", view.Availability, status, reason)
	}
	return view.Availability
}

func (f *projectionFixture) report(t *testing.T) devices.AvailabilityBatchWrite {
	t.Helper()
	write := testAvailabilityWrite(
		t,
		devices.AvailabilityBatchWrite{
			AdapterID:  "simulator",
			RuntimeID:  testRuntimeID,
			ReportedAt: f.at,
			Reports: []devices.EntityAvailabilityReport{
				{EntityID: f.entityID, Status: devices.EntityAvailabilityAvailable, SourceObservedAt: f.at},
			},
		},
	)
	if _, err := f.repository.ReportEntityAvailability(context.Background(), write); err != nil {
		t.Fatal(err)
	}
	return write
}

func TestHealthProjectionLagDoesNotAuthorizeCommands(t *testing.T) {
	t.Parallel()
	f := newProjectionFixture(t)
	f.availability(t, devices.EntityAvailabilityUnknown, "hearth.awaiting_adapter_projection")
	f.apply(t, f.head(t))
	f.report(t)
	f.availability(t, devices.EntityAvailabilityAvailable, "")
	f.at = f.at.Add(time.Second)
	f.heartbeat(t, adapters.AdapterHealthUnhealthy)
	instance, err := f.adapters.GetAdapter(context.Background(), "simulator")
	if err != nil || instance.Health.Status != adapters.AdapterHealthUnhealthy {
		t.Fatalf("canonical Adapter = %#v, %v", instance, err)
	}
	f.availability(t, devices.EntityAvailabilityAvailable, "")
	id, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlation, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	command, err := f.repository.CreateCommand(context.Background(), devices.CommandRecord{
		ID:            id,
		EntityID:      f.entityID,
		CorrelationID: correlation,
		OperationName: devices.OperationNameSet,
		Parameters: devices.CommandParameters(
			`{"power":true}`,
		),
		Status:      devices.CommandStatusRequested,
		RequestedAt: f.at,
		DeadlineAt:  f.at.Add(time.Second),
	})
	if err != nil || command.Status != devices.CommandStatusAdapterUnhealthy || command.RuntimeID != nil {
		t.Fatalf("command during projection lag = %#v, %v", command, err)
	}
	f.at = f.at.Add(10 * time.Second)
	f.apply(t, f.head(t))
	availability := f.availability(t, devices.EntityAvailabilityUnavailable, "adapter.simulator.offline")
	if !availability.Since.Equal(f.at) {
		t.Fatalf("since = %v, want application time %v", availability.Since, f.at)
	}
}

func TestHealthProjectionPreservesFutureReportAndSkipsObsoleteHistory(t *testing.T) {
	t.Parallel()
	f := newProjectionFixture(t)
	f.apply(t, f.head(t))
	f.report(t)
	f.at = f.at.Add(time.Second)
	f.heartbeat(t, adapters.AdapterHealthUnhealthy)
	older := f.head(t)
	f.at = f.at.Add(time.Second)
	f.heartbeat(t, adapters.AdapterHealthHealthy)
	newer := f.head(t)
	receipt := f.report(t)
	f.availability(t, devices.EntityAvailabilityUnknown, "hearth.awaiting_adapter_projection")
	f.apply(t, older)
	assertTableCount(t, f.database, "entity_availability_current", 1)
	f.apply(t, newer)
	f.availability(t, devices.EntityAvailabilityAvailable, "")
	before := projectionHistoryCount(t, f)
	result, err := f.repository.ApplyAdapterHealth(context.Background(), projectionUpdate(t, older), f.at)
	if err != nil || result.Disposition != devices.HealthProjectionObsolete {
		t.Fatalf("obsolete = %#v, %v", result, err)
	}
	if projectionHistoryCount(t, f) != before {
		t.Fatal("obsolete update added history")
	}
	assertTableCount(t, f.database, "entity_availability_current", 1)
	// A retried receipt retains its old generation rather than being restamped.
	f.at = f.at.Add(time.Second)
	f.heartbeat(t, adapters.AdapterHealthUnhealthy)
	f.at = f.at.Add(time.Second)
	f.heartbeat(t, adapters.AdapterHealthHealthy)
	f.apply(t, f.head(t))
	if _, err = f.repository.ReportEntityAvailability(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	f.availability(t, devices.EntityAvailabilityUnknown, "hearth.awaiting_entity_report")
	assertTableCount(t, f.database, "entity_availability_current", 0)
}

func TestHealthProjectionCanonicalDuplicateConflictAndCorruptEvidence(t *testing.T) {
	t.Parallel()
	f := newProjectionFixture(t)
	snapshot := f.head(t)
	f.apply(t, snapshot)
	before := projectionHistoryCount(t, f)
	// Stored spellings differ but describe the same exact snapshot.
	var stored string
	if err := f.database.QueryRow("SELECT snapshot_json FROM device_adapter_health_projection").
		Scan(&stored); err != nil {
		t.Fatal(err)
	}
	stored = strings.ReplaceAll(stored, `Z"`, `.000000000Z"`)
	fields := strings.Split(stored[1:len(stored)-1], ",")
	slices.Reverse(fields)
	stored = "{" + strings.Join(fields, ",") + "}"
	stored = " \n" + strings.ReplaceAll(stored, ",", ",\n ") + "\n"
	if _, err := f.database.Exec("UPDATE device_adapter_health_projection SET snapshot_json = ?", stored); err != nil {
		t.Fatal(err)
	}
	result, err := f.repository.ApplyAdapterHealth(context.Background(), projectionUpdate(t, snapshot), f.at)
	if err != nil || result.Disposition != devices.HealthProjectionDuplicate {
		t.Fatalf("semantic duplicate = %#v, %v", result, err)
	}
	conflict := snapshot
	conflict.EvidenceAt = conflict.EvidenceAt.Add(time.Nanosecond)
	if _, err = f.repository.ApplyAdapterHealth(
		context.Background(),
		projectionUpdate(t, conflict),
		f.at,
	); !errors.Is(
		err,
		devices.ErrHealthRevisionConflict,
	) {
		t.Fatalf("conflict error = %v", err)
	}
	if projectionHistoryCount(t, f) != before {
		t.Fatal("duplicate/conflict changed history")
	}
	if _, err = f.database.Exec("UPDATE device_adapter_health_projection SET snapshot_json = '{}' "); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.ApplyAdapterHealth(
		context.Background(),
		projectionUpdate(t, snapshot),
		f.at,
	); !errors.Is(
		err,
		devices.ErrCorruptHealthProjection,
	) {
		t.Fatalf("corrupt stored snapshot error = %v", err)
	}
}

func TestHealthProjectionWriteFailureRollsBackWithoutBecomingCorruption(t *testing.T) {
	t.Parallel()
	f := newProjectionFixture(t)
	f.apply(t, f.head(t))
	f.report(t)
	before := projectionHistoryCount(t, f)
	f.at = f.at.Add(time.Second)
	f.heartbeat(t, adapters.AdapterHealthUnhealthy)
	update := projectionUpdate(t, f.head(t))
	if _, err := f.database.Exec(
		`CREATE TRIGGER fail_effective BEFORE UPDATE ON entity_availability_effective BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repository.ApplyAdapterHealth(
		context.Background(),
		update,
		f.at,
	); err == nil ||
		errors.Is(err, devices.ErrCorruptHealthProjection) {
		t.Fatalf("storage failure = %v", err)
	}
	f.availability(t, devices.EntityAvailabilityAvailable, "")
	if projectionHistoryCount(t, f) != before {
		t.Fatal("failed projection committed history")
	}
	assertTableCount(t, f.database, "entity_availability_current", 1)
	if _, err := f.database.Exec("DROP TRIGGER fail_effective"); err != nil {
		t.Fatal(err)
	}
	result, err := f.repository.ApplyAdapterHealth(context.Background(), update, f.at)
	if err != nil || result.Disposition != devices.HealthProjectionApplied {
		t.Fatalf("replay = %#v, %v", result, err)
	}
	result, err = f.repository.ApplyAdapterHealth(context.Background(), update, f.at)
	if err != nil || result.Disposition != devices.HealthProjectionDuplicate {
		t.Fatalf("repeated replay = %#v, %v", result, err)
	}
	if projectionHistoryCount(t, f) != before+1 {
		t.Fatal("replay did not commit exactly one transition")
	}
}

//nolint:paralleltest,tparallel // Invalid attempts share one database and final no-mutation assertions.
func TestHealthProjectionRejectsFreelyConstructedInputsAtServiceAndRepository(t *testing.T) {
	t.Parallel()
	f := newProjectionFixture(t)
	valid := projectionUpdate(t, f.head(t))
	before := projectionHistoryCount(t, f)
	for _, test := range []struct {
		name   string
		mutate func(*devices.AdapterHealthUpdate)
	}{
		{"message", func(u *devices.AdapterHealthUpdate) { u.MessageID = "bad" }},
		{"adapter", func(u *devices.AdapterHealthUpdate) { u.Snapshot.AdapterID = "not.a.slug" }},
		{"revision", func(u *devices.AdapterHealthUpdate) { u.Snapshot.Revision = 0 }},
		{"runtime", func(u *devices.AdapterHealthUpdate) { u.Snapshot.RuntimeID = nil }},
		{"status", func(u *devices.AdapterHealthUpdate) { u.Snapshot.Status = "available" }},
		{"reason", func(u *devices.AdapterHealthUpdate) { u.Snapshot.Reason = &adapters.HealthReason{Code: "hearth.bad"} }},
		{"time", func(u *devices.AdapterHealthUpdate) { u.Snapshot.ChangedAt = time.Time{} }},
		{"zone", func(u *devices.AdapterHealthUpdate) {
			u.Snapshot.EvidenceAt = u.Snapshot.EvidenceAt.In(time.FixedZone("other", 3600))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			update := valid
			test.mutate(&update)
			if _, err := f.service.ApplyAdapterHealth(
				context.Background(),
				update,
			); !errors.Is(
				err,
				devices.ErrInvalidAdapterHealthUpdate,
			) {
				t.Fatalf("service input error = %v", err)
			}
			if _, err := f.repository.ApplyAdapterHealth(
				context.Background(),
				update,
				f.at,
			); !errors.Is(
				err,
				devices.ErrInvalidAdapterHealthUpdate,
			) {
				t.Fatalf("repository input error = %v", err)
			}
		})
	}
	if _, err := f.repository.ApplyAdapterHealth(
		context.Background(),
		valid,
		time.Time{},
	); !errors.Is(
		err,
		devices.ErrInvalidAdapterHealthUpdate,
	) {
		t.Fatalf("application time error = %v", err)
	}
	assertTableCount(t, f.database, "device_adapter_health_projection", 0)
	if projectionHistoryCount(t, f) != before {
		t.Fatal("invalid update wrote history")
	}
}

func projectionHistoryCount(t *testing.T, f *projectionFixture) int {
	t.Helper()
	var count int
	if err := f.database.QueryRow("SELECT count(*) FROM health_transitions WHERE resource_kind = 'entity' AND entity_id = ?", f.entityID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestHealthProjectionClassifiesFractionalRetainedGenerationsAsCorrupt(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"device_adapter_health_projection", "entity_availability_current"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()
			f := newProjectionFixture(t)
			f.apply(t, f.head(t))
			f.report(t)
			column := "revision"
			if table == "entity_availability_current" {
				column = "health_revision"
			}
			if _, err := f.database.Exec("UPDATE " + table + " SET " + column + " = 1.5"); err == nil {
				t.Fatal("schema accepted a fractional generation")
			}
			// Simulate already-corrupt retained evidence, including the case where
			// its SQLite storage type cannot scan directly into an int64.
			injectFractionalGeneration(t, f.database, table, column)
			before := projectionHistoryCount(t, f)
			f.at = f.at.Add(time.Second)
			f.heartbeat(t, adapters.AdapterHealthUnhealthy)
			if _, err := f.repository.ApplyAdapterHealth(
				context.Background(),
				projectionUpdate(t, f.head(t)),
				f.at,
			); !errors.Is(
				err,
				devices.ErrCorruptHealthProjection,
			) {
				t.Fatalf("retained generation corruption = %v", err)
			}
			if projectionHistoryCount(t, f) != before {
				t.Fatal("corrupt evidence partially committed history")
			}
		})
	}
}

func injectFractionalGeneration(t *testing.T, database *sql.DB, table, column string) {
	t.Helper()
	if _, err := database.Exec("PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE " + table + " SET " + column + " = 1.5"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("PRAGMA ignore_check_constraints = OFF"); err != nil {
		t.Fatal(err)
	}
}

func TestHealthProjectionBootstrapReusesHistoryAndEquivalentEvidencePreservesSince(t *testing.T) {
	t.Parallel()
	f := newProjectionFixture(t)
	healthy := f.head(t)
	f.apply(t, healthy)
	f.report(t)
	before := projectionHistoryCount(t, f)
	available := f.availability(t, devices.EntityAvailabilityAvailable, "")
	for _, table := range []string{"entity_availability_effective", "device_adapter_health_projection"} {
		if _, err := f.database.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	f.at = f.at.Add(time.Second)
	f.apply(t, healthy)
	restored := f.availability(t, devices.EntityAvailabilityAvailable, "")
	if !restored.Since.Equal(available.Since) || projectionHistoryCount(t, f) != before {
		t.Fatal("bootstrap invented an equivalent transition")
	}
	f.at = f.at.Add(time.Second)
	f.report(t)
	refreshed := f.availability(t, devices.EntityAvailabilityAvailable, "")
	if !refreshed.Since.Equal(available.Since) || !refreshed.EvidenceAt.Equal(f.at) ||
		projectionHistoryCount(t, f) != before {
		t.Fatal("equivalent report did not preserve since/history while refreshing evidence")
	}
}
