package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

type availabilityModelOperation uint8

const (
	modelHeartbeatHealthy availabilityModelOperation = iota
	modelHeartbeatUnhealthyNetwork
	modelHeartbeatUnhealthyAuthentication
	modelReportAvailable
	modelReportUnavailableNetwork
	modelReportUnavailableAuthentication
)

func (operation availabilityModelOperation) String() string {
	return [...]string{
		"heartbeat healthy",
		"heartbeat unhealthy network",
		"heartbeat unhealthy authentication",
		"report available",
		"report unavailable network",
		"report unavailable authentication",
	}[operation]
}

func (operation availabilityModelOperation) heartbeat() (devices.AdapterHealthStatus, string, bool) {
	switch operation {
	case modelHeartbeatHealthy:
		return devices.AdapterHealthHealthy, "", true
	case modelHeartbeatUnhealthyNetwork:
		return devices.AdapterHealthUnhealthy, "hearth.network_unreachable", true
	case modelHeartbeatUnhealthyAuthentication:
		return devices.AdapterHealthUnhealthy, "hearth.authentication_failed", true
	case modelReportAvailable, modelReportUnavailableNetwork, modelReportUnavailableAuthentication:
		return "", "", false
	}
	panic(fmt.Sprintf("unknown availability model operation %d", operation))
}

func (operation availabilityModelOperation) report() (devices.EntityAvailabilityStatus, string) {
	switch operation {
	case modelReportAvailable:
		return devices.EntityAvailabilityAvailable, ""
	case modelReportUnavailableNetwork:
		return devices.EntityAvailabilityUnavailable, "hearth.network_unreachable"
	case modelReportUnavailableAuthentication:
		return devices.EntityAvailabilityUnavailable, "hearth.authentication_failed"
	case modelHeartbeatHealthy, modelHeartbeatUnhealthyNetwork, modelHeartbeatUnhealthyAuthentication:
		panic(fmt.Sprintf("non-report availability model operation %d", operation))
	}
	panic(fmt.Sprintf("unknown availability model operation %d", operation))
}

type availabilityModelReport struct {
	status           devices.EntityAvailabilityStatus
	reasonCode       string
	since            time.Time
	evidenceAt       time.Time
	sourceObservedAt time.Time
}

type availabilityModelTransition struct {
	status           string
	source           string
	reasonCode       string
	sourceObservedAt *time.Time
	observedAt       time.Time
}

type availabilityHistoryModel struct {
	adapterStatus         devices.AdapterHealthStatus
	adapterReason         string
	adapterSince          time.Time
	adapterEvidence       time.Time
	adapterSourceObserved *time.Time
	report                *availabilityModelReport
	candidates            []availabilityModelTransition
}

func newAvailabilityHistoryModel(claimedAt, registeredAt time.Time) *availabilityHistoryModel {
	baselineAt := registeredAt
	if claimedAt.After(baselineAt) {
		baselineAt = claimedAt
	}
	return &availabilityHistoryModel{
		adapterStatus:   devices.AdapterHealthUnknown,
		adapterReason:   "hearth.awaiting_health",
		adapterSince:    baselineAt,
		adapterEvidence: baselineAt,
		candidates: []availabilityModelTransition{{
			status:     string(devices.EntityAvailabilityUnknown),
			source:     "adapter_health",
			reasonCode: "hearth.awaiting_health",
			observedAt: registeredAt,
		}},
	}
}

func (model *availabilityHistoryModel) apply(operation availabilityModelOperation, at time.Time) bool {
	if status, reasonCode, heartbeat := operation.heartbeat(); heartbeat {
		model.applyHeartbeat(status, reasonCode, at)
		return true
	}
	status, reasonCode := operation.report()
	return model.applyReport(status, reasonCode, at)
}

func (model *availabilityHistoryModel) applyHeartbeat(
	status devices.AdapterHealthStatus,
	reasonCode string,
	at time.Time,
) {
	changed := model.adapterStatus != status || model.adapterReason != reasonCode
	if model.adapterStatus == devices.AdapterHealthHealthy && status != devices.AdapterHealthHealthy {
		model.report = nil
	}
	model.adapterStatus = status
	model.adapterReason = reasonCode
	model.adapterEvidence = at
	sourceObservedAt := at
	model.adapterSourceObserved = &sourceObservedAt
	if !changed {
		return
	}
	model.adapterSince = at
	transition := availabilityModelTransition{observedAt: at}
	if status == devices.AdapterHealthHealthy {
		transition.status = string(devices.EntityAvailabilityUnknown)
		transition.source = healthSourceCore
		transition.reasonCode = "hearth.awaiting_entity_report"
	} else {
		transition.status = string(devices.EntityAvailabilityUnavailable)
		transition.source = "adapter_health"
		transition.reasonCode = reasonCode
		transition.sourceObservedAt = &sourceObservedAt
	}
	model.candidates = append(model.candidates, transition)
}

func (model *availabilityHistoryModel) applyReport(
	status devices.EntityAvailabilityStatus,
	reasonCode string,
	at time.Time,
) bool {
	if model.adapterStatus != devices.AdapterHealthHealthy {
		return false
	}
	changed := model.report == nil || model.report.status != status || model.report.reasonCode != reasonCode
	since := at
	if !changed {
		since = model.report.since
	}
	model.report = &availabilityModelReport{
		status: status, reasonCode: reasonCode, since: since, evidenceAt: at, sourceObservedAt: at,
	}
	if changed {
		sourceObservedAt := at
		model.candidates = append(model.candidates, availabilityModelTransition{
			status: string(status), source: "entity_report", reasonCode: reasonCode,
			sourceObservedAt: &sourceObservedAt, observedAt: at,
		})
	}
	return true
}

func (model *availabilityHistoryModel) current() devices.EntityAvailability {
	if model.adapterStatus == devices.AdapterHealthHealthy && model.report != nil {
		report := model.report
		return devices.EntityAvailability{
			Status: report.status, Source: "entity_report", Since: report.since, EvidenceAt: report.evidenceAt,
			SourceObservedAt: &report.sourceObservedAt, Reason: modelReason(report.reasonCode),
		}
	}
	status := devices.EntityAvailabilityUnknown
	source := "adapter_health"
	reasonCode := model.adapterReason
	switch model.adapterStatus {
	case devices.AdapterHealthHealthy:
		source = healthSourceCore
		reasonCode = "hearth.awaiting_entity_report"
	case devices.AdapterHealthUnhealthy:
		status = devices.EntityAvailabilityUnavailable
	case devices.AdapterHealthUnknown:
	}
	var sourceObservedAt *time.Time
	if model.adapterStatus != devices.AdapterHealthHealthy {
		sourceObservedAt = model.adapterSourceObserved
	}
	return devices.EntityAvailability{
		Status: status, Source: source, Since: model.adapterSince, EvidenceAt: model.adapterEvidence,
		SourceObservedAt: sourceObservedAt, Reason: modelReason(reasonCode),
	}
}

func (model *availabilityHistoryModel) effectiveHistory() []availabilityModelTransition {
	chronological := make([]availabilityModelTransition, 0, len(model.candidates))
	for _, candidate := range model.candidates {
		if len(chronological) == 0 || candidate.status != chronological[len(chronological)-1].status ||
			candidate.reasonCode != chronological[len(chronological)-1].reasonCode {
			chronological = append(chronological, candidate)
		}
	}
	newestFirst := make([]availabilityModelTransition, len(chronological))
	for index := range chronological {
		newestFirst[len(chronological)-1-index] = chronological[index]
	}
	return newestFirst
}

func TestSQLiteEntityAvailabilityHistoryMatchesReferenceModel(t *testing.T) {
	t.Parallel()
	databaseImage := newAvailabilityPropertyDatabaseImage(t)
	catalog := firstLightCatalog(t)
	operations := []availabilityModelOperation{
		modelHeartbeatHealthy,
		modelHeartbeatUnhealthyNetwork,
		modelHeartbeatUnhealthyAuthentication,
		modelReportAvailable,
		modelReportUnavailableNetwork,
		modelReportUnavailableAuthentication,
	}
	preparedPath := filepath.Join(t.TempDir(), "prepared.db")
	prepared := false
	rapid.Check(t, func(t *rapid.T) {
		generated := rapid.SliceOfN(rapid.SampledFrom(operations), 0, 24).Draw(t, "operations")
		pageLimit := rapid.IntRange(1, 5).Draw(t, "page limit")
		repository, entityID, claimedAt, registeredAt := newAvailabilityPropertyFixture(
			t, databaseImage, catalog, prepared,
		)
		model := newAvailabilityHistoryModel(claimedAt, registeredAt)

		at := registeredAt
		for _, operation := range []availabilityModelOperation{
			modelHeartbeatHealthy,
			modelReportUnavailableNetwork,
			modelReportUnavailableNetwork,
			modelHeartbeatUnhealthyNetwork,
			modelHeartbeatUnhealthyAuthentication,
			modelHeartbeatHealthy,
		} {
			at = at.Add(time.Second)
			if prepared {
				model.apply(operation, at)
			} else {
				applyAvailabilityPropertyOperation(t, repository, entityID, model, operation, at)
			}
		}
		if !prepared {
			// Check the fixed prefix once, then snapshot its committed database.
			// Each generated sequence still gets an isolated database and model.
			assertAvailabilityPropertyHistory(t, repository, entityID, model.effectiveHistory(), 1)
			if _, err := repository.database.ExecContext(t.Context(), "VACUUM INTO ?", preparedPath); err != nil {
				t.Fatal(err)
			}
			image, err := os.ReadFile(preparedPath)
			if err != nil {
				t.Fatal(err)
			}
			databaseImage = image
			prepared = true
		}
		for _, operation := range generated {
			at = at.Add(time.Second)
			applyAvailabilityPropertyOperation(t, repository, entityID, model, operation, at)
		}

		assertAvailabilityPropertyHistory(t, repository, entityID, model.effectiveHistory(), pageLimit)
	})
}

func newAvailabilityPropertyDatabaseImage(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "template.db")
	database, err := platformdb.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if migrationErr := platformdb.Migrate(t.Context(), database); migrationErr != nil {
		_ = database.Close()
		t.Fatal(migrationErr)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func newAvailabilityPropertyFixture(
	t *rapid.T,
	databaseImage []byte,
	catalog *devices.TypeCatalog,
	prepared bool,
) (*DeviceRepository, devices.EntityID, time.Time, time.Time) {
	t.Helper()
	directory, err := os.MkdirTemp("", "hearth-availability-property-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "hearth.db")
	if writeErr := os.WriteFile(path, databaseImage, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	database, err := platformdb.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	registeredAt := claimedAt.Add(time.Second)
	entityID := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ae")
	if prepared {
		return repository, entityID, claimedAt, registeredAt
	}
	if claimErr := repository.ClaimAdapterRuntime(t.Context(), devices.ClaimRuntimeWrite{
		RuntimeID: testRuntimeID, AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(time.Hour),
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	service := newTestService(repository, nil, catalog, devices.Dependencies{
		Now: func() time.Time { return registeredAt },
		NewDeviceID: func() (devices.DeviceID, error) {
			return devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ae"), nil
		},
		NewEntityID: func() (devices.EntityID, error) {
			return entityID, nil
		},
	})
	binding, err := service.Register(t.Context(), "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	return repository, binding.Entities[0].EntityID, claimedAt, registeredAt
}

func applyAvailabilityPropertyOperation(
	t *rapid.T,
	repository *DeviceRepository,
	entityID devices.EntityID,
	model *availabilityHistoryModel,
	operation availabilityModelOperation,
	at time.Time,
) {
	t.Helper()
	wantAccepted := model.apply(operation, at)
	var err error
	if status, reasonCode, heartbeat := operation.heartbeat(); heartbeat {
		_, err = repository.RecordAdapterHeartbeat(t.Context(), devices.HeartbeatWrite{
			AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: status,
			SourceObservedAt: at, Reason: modelReason(reasonCode), ReceivedAt: at,
			LeaseExpiresAt: at.Add(time.Hour),
		})
	} else {
		status, reportReasonCode := operation.report()
		_, err = repository.ReportEntityAvailability(
			t.Context(), testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
				AdapterID: "simulator", RuntimeID: testRuntimeID, ReportedAt: at,
				Reports: []devices.EntityAvailabilityReport{{
					EntityID: entityID, Status: status, SourceObservedAt: at, Reason: modelReason(reportReasonCode),
				}},
			}))
	}
	if wantAccepted && err != nil {
		t.Fatalf("%s at %s: %v", operation, at.Format(time.RFC3339), err)
	}
	if !wantAccepted && !errors.Is(err, devices.ErrAdapterUnhealthy) {
		t.Fatalf("%s at %s error = %v, want Adapter unhealthy", operation, at.Format(time.RFC3339), err)
	}
	view, err := repository.GetEntity(t.Context(), entityID)
	if err != nil {
		t.Fatal(err)
	}
	assertAvailabilityPropertyCurrent(t, operation, model.current(), view.Availability)
}

func assertAvailabilityPropertyCurrent(
	t *rapid.T,
	operation availabilityModelOperation,
	want devices.EntityAvailability,
	got devices.EntityAvailability,
) {
	t.Helper()
	if got.Status != want.Status || got.Source != want.Source ||
		modelReasonCode(got.Reason) != modelReasonCode(want.Reason) ||
		!got.Since.Equal(want.Since) || !got.EvidenceAt.Equal(want.EvidenceAt) ||
		!optionalTimesEqual(got.SourceObservedAt, want.SourceObservedAt) {
		t.Fatalf("after %s current availability = %#v, want %#v", operation, got, want)
	}
}

func assertAvailabilityPropertyHistory(
	t *rapid.T,
	repository *DeviceRepository,
	entityID devices.EntityID,
	want []availabilityModelTransition,
	pageLimit int,
) {
	t.Helper()
	unpaged, err := repository.ListEntityAvailabilityHistory(t.Context(), devices.ListEntityAvailabilityParams{
		EntityID: entityID, Limit: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unpaged.HasMore {
		t.Fatalf("%d transitions unexpectedly exceeded the unpaged limit", len(unpaged.Items))
	}
	assertAvailabilityPropertyTransitions(t, unpaged.Items, want)

	var paged []devices.HealthTransition
	var before *int64
	for {
		page, pageErr := repository.ListEntityAvailabilityHistory(t.Context(), devices.ListEntityAvailabilityParams{
			EntityID: entityID, BeforeReceiveOrder: before, Limit: pageLimit,
		})
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		paged = append(paged, page.Items...)
		if !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			t.Fatal("history page has more results but no cursor position")
		}
		cursor := page.Items[len(page.Items)-1].ReceiveOrder
		before = &cursor
	}
	assertAvailabilityPropertyTransitions(t, paged, want)
}

func assertAvailabilityPropertyTransitions(
	t *rapid.T,
	got []devices.HealthTransition,
	want []availabilityModelTransition,
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("history length = %d, want %d\ngot: %#v\nwant: %#v", len(got), len(want), got, want)
	}
	for index := range want {
		if got[index].ReceiveOrder < 1 ||
			(index > 0 && got[index-1].ReceiveOrder <= got[index].ReceiveOrder) ||
			got[index].Status != want[index].status || got[index].Source != want[index].source ||
			modelReasonCode(got[index].Reason) != want[index].reasonCode ||
			!optionalTimesEqual(got[index].SourceObservedAt, want[index].sourceObservedAt) ||
			!got[index].ObservedAt.Equal(want[index].observedAt) {
			t.Fatalf("history[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func modelReason(code string) *devices.HealthReason {
	if code == "" {
		return nil
	}
	return &devices.HealthReason{Code: code}
}

func modelReasonCode(reason *devices.HealthReason) string {
	if reason == nil {
		return ""
	}
	return reason.Code
}

func optionalTimesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
