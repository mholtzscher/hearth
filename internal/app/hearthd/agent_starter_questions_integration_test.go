//go:build integration

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/household"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// starterDigestModel answers a starter question pass with a fixed reply and
// keeps the digest it was sent.
type starterDigestModel struct {
	mutex  sync.Mutex
	digest string
}

func (fake *starterDigestModel) Generate(
	_ context.Context,
	messages []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	fake.mutex.Lock()
	fake.digest = messages[len(messages)-1].Content
	fake.mutex.Unlock()
	return schema.AssistantMessage(`{"starter_questions":[`+
		`{"title":"Why is the Garage Lock offline?","prompt":"Why is the Garage Lock unavailable?"},`+
		`{"title":"Show the office temperature","prompt":"Show the office temperature today."},`+
		`{"title":"What happened overnight?","prompt":"What happened in the house overnight?"}]}`, nil), nil
}

func (fake *starterDigestModel) Stream(
	context.Context, []*schema.Message, ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("starterDigestModel: Stream is not supported")
}

func (fake *starterDigestModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return fake, nil
}

// This test protects the assembly between Devices persistence, the household
// digest reader, and the agent cache. It fails if the reader drops an
// unavailable Entity, reports a reading in canonical instead of household
// units, includes an Entity whose availability is unknown, or a pass does not
// reach the cache.
func TestStarterQuestionPassReadsHouseholdFromPersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	defer func() { _ = database.Close() }()
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	deviceService := devices.NewService(
		testDeviceStores(devicessqlite.NewDeviceRepository(database, catalog), database), nil, catalog,
		devices.Dependencies{},
	)
	runtimeID := adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	adapterService := adapterServiceForTest(database)
	if claimErr := adapterService.ClaimAdapterRuntime(ctx, adapters.ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: runtimeID, SoftwareName: "hearth-starter-test", SoftwareVersion: "0.1.0",
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	binding, err := deviceService.Register(ctx, "simulator", runtimeID, devices.Registration{
		BindingKey: "office",
		Device:     devices.DeviceDescriptor{Name: "Office Sensor", Kind: devices.DeviceKindSensor},
		Entities: []devices.EntityDescriptor{
			{
				Key: "temperature", ExternalID: "office.temperature", Name: "Office Temperature",
				TypeID: devices.EntityTypeTemperatureV1, Support: devices.EntitySupport(`{"state":{},"operations":{}}`),
			},
			{
				Key: "lock", ExternalID: "office.lock", Name: "Garage Lock",
				TypeID:  devices.EntityTypeLockV1,
				Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			},
			{
				Key:        "contact",
				ExternalID: "office.contact",
				Name:       "Silent Contact",
				TypeID:     devices.EntityTypeBinarysensorV1,
				Support:    devices.EntitySupport(`{"state":{},"operations":{}}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	temperatureID, lockID := binding.Entities[0].EntityID, binding.Entities[1].EntityID
	now := time.Now().UTC()
	// Entity availability reports need a healthy Adapter: record a heartbeat
	// and project its pending health update as the relay would.
	if _, heartbeatErr := adapterService.RecordAdapterHeartbeat(ctx, adapters.AdapterHeartbeat{
		AdapterID:        "simulator",
		RuntimeID:        runtimeID,
		ExternalStatus:   adapters.AdapterHealthHealthy,
		SourceObservedAt: now,
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}
	adapterRepository := adapterssqlite.NewAdapterRepository(database)
	pending, err := adapterRepository.ListPendingHealthUpdates(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range pending {
		if _, applyErr := deviceService.ApplyAdapterHealth(ctx, devices.AdapterHealthUpdate{
			MessageID: string(update.ID), Snapshot: update.Snapshot,
		}); applyErr != nil {
			t.Fatal(applyErr)
		}
	}
	if _, projectErr := deviceService.ProjectObservation(ctx, "simulator", runtimeID, devices.Observation{
		ID:            devices.ObservationID("obs_" + uuid.Must(uuid.NewV7()).String()),
		CorrelationID: devices.CorrelationID("cor_" + uuid.Must(uuid.NewV7()).String()),
		EntityID:      temperatureID, Value: devices.Value(`21500`), AdapterReceivedAt: now,
	}, now); projectErr != nil {
		t.Fatal(projectErr)
	}
	if _, availabilityErr := deviceService.ReportEntityAvailability(
		ctx, "avl_"+uuid.Must(uuid.NewV7()).String(), "simulator", runtimeID,
		[]devices.EntityAvailabilityReport{
			{EntityID: temperatureID, Status: devices.EntityAvailabilityAvailable, SourceObservedAt: now},
			{
				EntityID:         lockID,
				Status:           devices.EntityAvailabilityUnavailable,
				SourceObservedAt: now,
				Reason:           &devices.HealthReason{Code: "hearth.entity_unavailable"},
			},
		},
	); availabilityErr != nil {
		t.Fatal(availabilityErr)
	}
	automationService := automations.NewService(
		automationssqlite.NewAutomationRepository(
			database,
			automations.Dependencies{},
		),
		nil,
		automations.Dependencies{},
	)

	chat := &starterDigestModel{}
	agentService, err := agent.NewService(ctx, agent.Config{
		DB: database, Tools: []tool.BaseTool{stubAgentTool{}}, ChatModel: chat,
		Retention: agent.MinimumConversationRetention, Logger: slog.New(slog.DiscardHandler),
		HouseholdDigest: newHouseholdDigestReader(
			deviceService, automationService, household.Settings{UnitSystem: household.UnitSystemUS}, time.UTC,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshErr := agentService.RefreshStarterQuestions(ctx); refreshErr != nil {
		t.Fatal(refreshErr)
	}

	chat.mutex.Lock()
	digest := chat.digest
	chat.mutex.Unlock()
	for _, want := range []string{
		"Unavailable Entities (1):\n- Garage Lock [hearth.lock/v1] on Office Sensor",
		"- Office Temperature [hearth.temperature/v1] on Office Sensor: 70.7 (value in °F)",
		"Unit system: us",
	} {
		if !strings.Contains(digest, want) {
			t.Fatalf("digest lacks %q:\n%s", want, digest)
		}
	}
	if strings.Contains(digest, "Silent Contact") {
		t.Fatalf("digest includes an Entity with unknown availability:\n%s", digest)
	}
	cached := agentService.StarterQuestions()
	if len(cached.Questions) != 3 || cached.Questions[0].Title != "Why is the Garage Lock offline?" ||
		cached.GeneratedAt.IsZero() {
		t.Fatalf("cache = %+v", cached)
	}
}
