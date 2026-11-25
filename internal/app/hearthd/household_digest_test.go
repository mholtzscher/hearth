package hearthd //nolint:testpackage // Tests exercise the package-private digest reader.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

// fakeDigestDevices pages its fixed Devices and Entities one item at a time,
// so the reader must follow cursors to see everything.
type fakeDigestDevices struct {
	devices  []devices.Device
	entities []devices.EntityWithState
	err      error
}

func (fake *fakeDigestDevices) ListDevices(
	_ context.Context,
	params devices.ListDevicesParams,
) (devices.Page[devices.Device], error) {
	if fake.err != nil {
		return devices.Page[devices.Device]{}, fake.err
	}
	start := 0
	for index, device := range fake.devices {
		if params.AfterID != nil && device.ID == *params.AfterID {
			start = index + 1
		}
	}
	if start >= len(fake.devices) {
		return devices.Page[devices.Device]{}, nil
	}
	return devices.Page[devices.Device]{Items: fake.devices[start : start+1], HasMore: start+1 < len(fake.devices)}, nil
}

func (fake *fakeDigestDevices) ListEntities(
	_ context.Context,
	params devices.ListEntitiesParams,
) (devices.Page[devices.EntityWithState], error) {
	start := 0
	for index, view := range fake.entities {
		if params.AfterID != nil && view.Entity.ID == *params.AfterID {
			start = index + 1
		}
	}
	if start >= len(fake.entities) {
		return devices.Page[devices.EntityWithState]{}, nil
	}
	return devices.Page[devices.EntityWithState]{
		Items: fake.entities[start : start+1], HasMore: start+1 < len(fake.entities),
	}, nil
}

type fakeDigestAutomations struct {
	records []automations.Record
	history map[automations.AutomationID][]automations.HistorySummary
	err     error
}

func (fake *fakeDigestAutomations) ListAutomations(
	context.Context,
	automations.ListAutomationsParams,
) (automations.Page[automations.Record], error) {
	return automations.Page[automations.Record]{Items: fake.records}, nil
}

func (fake *fakeDigestAutomations) ListHistory(
	_ context.Context,
	params automations.ListHistoryParams,
) (automations.Page[automations.HistorySummary], error) {
	if fake.err != nil {
		return automations.Page[automations.HistorySummary]{}, fake.err
	}
	return automations.Page[automations.HistorySummary]{Items: fake.history[params.AutomationID]}, nil
}

func digestView(
	id, deviceID, name string,
	entityType devices.EntityTypeID,
	status devices.EntityAvailabilityStatus,
	state string,
) devices.EntityWithState {
	view := devices.EntityWithState{
		Entity: devices.Entity{
			ID: devices.EntityID(id), DeviceID: devices.DeviceID(deviceID), Name: name,
			TypeID: entityType, Support: devices.EntitySupport(`{}`), Enabled: true,
		},
		Availability: devices.EntityAvailability{Status: status},
	}
	if state != "" {
		view.State = &devices.State{Value: devices.Value(state)}
	}
	return view
}

func TestHouseholdDigestReaderFiltersAndProjectsEntities(t *testing.T) {
	t.Parallel()
	override := "Office Thermometer"
	disabled := digestView(
		"ent_3",
		"dev_1",
		"Disabled",
		devices.EntityTypePowerV1,
		devices.EntityAvailabilityAvailable,
		`true`,
	)
	disabled.Entity.Enabled = false
	renamed := digestView(
		"ent_1",
		"dev_1",
		"Temperature",
		devices.EntityTypeTemperatureV1,
		devices.EntityAvailabilityAvailable,
		`21500`,
	)
	renamed.Entity.NameOverride = &override
	location := time.FixedZone("CST", -6*60*60)
	now := time.Date(2026, 11, 25, 1, 42, 0, 0, time.UTC)
	reader := newHouseholdDigestReader(
		&fakeDigestDevices{
			devices: []devices.Device{{ID: "dev_1", Name: "Office Sensor"}, {ID: "dev_2", Name: "Garage"}},
			entities: []devices.EntityWithState{
				renamed,
				digestView(
					"ent_2",
					"dev_2",
					"Garage Lock",
					devices.EntityTypeLockV1,
					devices.EntityAvailabilityUnavailable,
					"",
				),
				disabled,
				digestView(
					"ent_4",
					"dev_2",
					"Garage Door",
					devices.EntityTypeBinarysensorV1,
					devices.EntityAvailabilityUnknown,
					`true`,
				),
			},
		},
		&fakeDigestAutomations{},
		household.Settings{UnitSystem: household.UnitSystemUS},
		location,
	)
	reader.now = func() time.Time { return now }
	digest, err := reader.ReadHouseholdDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !digest.LocalTime.Equal(now) || digest.LocalTime.Location() != location ||
		digest.UnitSystem != household.UnitSystemUS {
		t.Fatalf("digest time and units = %v %s", digest.LocalTime, digest.UnitSystem)
	}
	if len(digest.Entities) != 2 {
		t.Fatalf("entities = %+v, want the enabled Entities with known availability", digest.Entities)
	}
	temperature := digest.Entities[0]
	if temperature.Name != "Office Thermometer" || temperature.Device != "Office Sensor" ||
		temperature.Availability != agent.DigestAvailable || temperature.Units[""] != "°F" {
		t.Fatalf("temperature = %+v", temperature)
	}
	if string(temperature.State) != "70.7" {
		t.Fatalf("temperature State = %s, want 70.7 rounded to its display precision", temperature.State)
	}
	lock := digest.Entities[1]
	if lock.Name != "Garage Lock" || lock.Device != "Garage" || lock.Availability != agent.DigestUnavailable ||
		lock.State != nil {
		t.Fatalf("lock = %+v", lock)
	}
}

func TestHouseholdDigestReaderReportsRecentAutomationProblems(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC)
	porch := automations.AutomationID("aut_porch")
	entry := func(age time.Duration, body automations.HistorySummaryBody) automations.HistorySummary {
		return automations.HistorySummary{AutomationID: porch, RecordedAt: now.Add(-age), Body: body}
	}
	reader := newHouseholdDigestReader(
		&fakeDigestDevices{},
		&fakeDigestAutomations{
			records: []automations.Record{
				{ID: porch, Definition: automations.Definition{Name: "Porch lights", Enabled: true}},
				{ID: "aut_quiet", Definition: automations.Definition{Name: "Quiet hours", Enabled: false}},
			},
			history: map[automations.AutomationID][]automations.HistorySummary{
				porch: {
					entry(time.Hour, automations.RunHistorySummary{Status: automations.RunSucceeded}),
					entry(2*time.Hour, automations.RunHistorySummary{Status: automations.RunFailed}),
					entry(3*time.Hour, automations.SkipHistorySummary{Reason: automations.SkipConditionsFalse}),
					entry(4*time.Hour, automations.SkipHistorySummary{Reason: automations.SkipConditionsUnknown}),
					entry(5*time.Hour, automations.RunHistorySummary{Status: automations.RunInterrupted}),
					entry(6*time.Hour, automations.RunHistorySummary{Status: automations.RunFailed}),
				},
				"aut_quiet": {
					entry(25*time.Hour, automations.RunHistorySummary{Status: automations.RunFailed}),
				},
			},
		},
		household.Settings{UnitSystem: household.UnitSystemMetric},
		time.UTC,
	)
	reader.now = func() time.Time { return now }
	digest, err := reader.ReadHouseholdDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.Automations) != 2 {
		t.Fatalf("automations = %+v", digest.Automations)
	}
	porchDigest := digest.Automations[0]
	wantKinds := []string{
		agent.DigestProblemFailed,
		agent.DigestProblemConditionsUnknown,
		agent.DigestProblemInterrupted,
	}
	if porchDigest.Name != "Porch lights" || !porchDigest.Enabled || len(porchDigest.Problems) != len(wantKinds) {
		t.Fatalf("porch = %+v", porchDigest)
	}
	for index, kind := range wantKinds {
		if porchDigest.Problems[index].Kind != kind {
			t.Fatalf("porch problem %d = %s, want %s", index, porchDigest.Problems[index].Kind, kind)
		}
	}
	if !porchDigest.Problems[0].At.Equal(now.Add(-2 * time.Hour)) {
		t.Fatalf("first problem at %v", porchDigest.Problems[0].At)
	}
	quiet := digest.Automations[1]
	if quiet.Enabled || len(quiet.Problems) != 0 {
		t.Fatalf("quiet = %+v, want no problems older than 24 hours", quiet)
	}
}

func TestHouseholdDigestReaderFailsOnReadError(t *testing.T) {
	t.Parallel()
	readErr := errors.New("database unavailable")
	for name, reader := range map[string]householdDigestReader{
		"devices": newHouseholdDigestReader(
			&fakeDigestDevices{err: readErr}, &fakeDigestAutomations{}, household.Settings{}, time.UTC,
		),
		"automation history": newHouseholdDigestReader(
			&fakeDigestDevices{},
			&fakeDigestAutomations{records: []automations.Record{{ID: "aut_1"}}, err: readErr},
			household.Settings{}, time.UTC,
		),
	} {
		if _, err := reader.ReadHouseholdDigest(context.Background()); !errors.Is(err, readErr) {
			t.Fatalf("%s: err = %v, want the read error", name, err)
		}
	}
}

func TestRoundQuantitiesUsesDisplayPrecision(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		state    string
		metadata map[string]devices.QuantityMetadata
		want     string
	}{
		"root":         {`70.69999999999999`, map[string]devices.QuantityMetadata{"": {FractionDigits: 1}}, `70.7`},
		"object field": {`{"current":70.69999999999999,"mode":"heat"}`, map[string]devices.QuantityMetadata{"/current": {FractionDigits: 0}}, `{"current":71,"mode":"heat"}`},
		"nested path":  {`{"a":{"b":1.234}}`, map[string]devices.QuantityMetadata{"/a/b": {FractionDigits: 1}}, `{"a":{"b":1.234}}`},
		"non-number":   {`"locked"`, map[string]devices.QuantityMetadata{"": {FractionDigits: 1}}, `"locked"`},
		"no metadata":  {`1.23456`, nil, `1.23456`},
	} {
		if got := string(roundQuantities([]byte(test.state), test.metadata)); got != test.want {
			t.Fatalf("%s: got %s, want %s", name, got, test.want)
		}
	}
}
