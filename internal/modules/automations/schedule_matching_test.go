package automations_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func cronDefinition(t *testing.T, expression string) automations.Definition {
	t.Helper()
	definition := validDomainDefinition(t)
	definition.Triggers = []automations.Trigger{
		{ID: "scheduled", Body: automations.CronTrigger{Expression: expression}},
	}
	return definition
}

// storedCronDefinition exercises preparation retained by repository decoding.
func storedCronDefinition(t *testing.T, expression string) automations.Definition {
	t.Helper()
	raw, err := automations.EncodeDefinition(cronDefinition(t, expression))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := automations.DecodeDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

// These UTC/local pairs are literal fixtures from A2 and A3, not cron.Next results.
// They protect clock-field semantics and fail if matching suppresses a DST fold,
// shifts a missing local time, treats a step as elapsed time, or loses field AND.
func TestScheduledTriggerMatchingClockAndDST(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, zone, expression, utc, local string
		want                               bool
	}{
		{"daily", "America/Chicago", "0 7 * * *", "2026-10-02T12:00:00Z", "2026-10-02 07:00", true},
		{"wrong minute", "America/Chicago", "0 7 * * *", "2026-10-02T12:01:00Z", "2026-10-02 07:01", false},
		{
			"weekday range mixed case",
			"America/Chicago",
			"0 7 * * mon-FrI",
			"2026-10-02T12:00:00Z",
			"2026-10-02 07:00",
			true,
		},
		{"weekday AND", "America/Chicago", "0 7 * * MON-THU", "2026-10-02T12:00:00Z", "2026-10-02 07:00", false},
		{"hour AND", "America/Chicago", "0 9 * * FRI", "2026-10-02T12:00:00Z", "2026-10-02 07:00", false},
		{"wildcard steps", "UTC", "*/15 */3 * * *", "2026-10-02T09:45:00Z", "2026-10-02 09:45", true},
		{"minute list", "UTC", "5,20,50 9 * * FRI", "2026-10-02T09:20:00Z", "2026-10-02 09:20", true},
		{"range steps lower bound", "UTC", "5-35/15 7-11/2 * * *", "2026-10-02T09:20:00Z", "2026-10-02 09:20", true},
		{"range steps exclude", "UTC", "5-35/15 7-11/2 * * *", "2026-10-02T08:20:00Z", "2026-10-02 08:20", false},
		{"value steps", "UTC", "5/15 7/4 * * FRI/1", "2026-10-02T23:50:00Z", "2026-10-02 23:50", true},
		{"weekday range steps", "UTC", "0 7 * * MON-FRI/2", "2026-10-02T07:00:00Z", "2026-10-02 07:00", true},
		{"lists midnight", "UTC", "00 0,23 * * FRI,SUN", "2026-10-02T00:00:00Z", "2026-10-02 00:00", true},
		{"lists 23", "UTC", "0 0,23 * * 5,0", "2026-10-02T23:00:00Z", "2026-10-02 23:00", true},
		{"seven minute last", "UTC", "*/7 * * * *", "2026-10-02T07:56:00Z", "2026-10-02 07:56", true},
		{"seven minute reset", "UTC", "*/7 * * * *", "2026-10-02T08:00:00Z", "2026-10-02 08:00", true},
		{"not elapsed seven minutes", "UTC", "*/7 * * * *", "2026-10-02T08:03:00Z", "2026-10-02 08:03", false},
		{"gap before", "America/Chicago", "30 2 * * *", "2026-03-08T07:30:00Z", "2026-03-08 01:30", false},
		{"gap no shift to 03", "America/Chicago", "30 2 * * *", "2026-03-08T08:00:00Z", "2026-03-08 03:00", false},
		{"gap no shift to 0330", "America/Chicago", "30 2 * * *", "2026-03-08T08:30:00Z", "2026-03-08 03:30", false},
		{"Chicago first fold", "America/Chicago", "30 1 * * *", "2026-11-01T06:30:00Z", "2026-11-01 01:30", true},
		{"Chicago second fold", "America/Chicago", "30 1 * * *", "2026-11-01T07:30:00Z", "2026-11-01 01:30", true},
		{
			"every minute before rollback",
			"America/Chicago",
			"* * * * *",
			"2026-11-01T06:59:00Z",
			"2026-11-01 01:59",
			true,
		},
		{
			"every minute after rollback",
			"America/Chicago",
			"* * * * *",
			"2026-11-01T07:00:00Z",
			"2026-11-01 01:00",
			true,
		},
		{
			"quarter hour first fold",
			"America/Chicago",
			"*/15 1 * * *",
			"2026-11-01T06:45:00Z",
			"2026-11-01 01:45",
			true,
		},
		{
			"quarter hour second fold",
			"America/Chicago",
			"*/15 1 * * *",
			"2026-11-01T07:45:00Z",
			"2026-11-01 01:45",
			true,
		},
		{"Lord Howe first fold", "Australia/Lord_Howe", "45 1 * * *", "2026-04-04T14:45:00Z", "2026-04-05 01:45", true},
		{
			"Lord Howe second fold",
			"Australia/Lord_Howe",
			"45 1 * * *",
			"2026-04-04T15:15:00Z",
			"2026-04-05 01:45",
			true,
		},
		{"historical local seconds", "Europe/Paris", "* * * * *", "1900-01-01T00:00:00Z", "1900-01-01 00:09", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			location, err := time.LoadLocation(test.zone)
			if err != nil {
				t.Fatal(err)
			}
			minute, err := time.Parse(time.RFC3339, test.utc)
			if err != nil {
				t.Fatal(err)
			}
			if got := minute.In(location).Format("2006-01-02 15:04"); got != test.local {
				t.Fatalf("fixture local = %s, want %s", got, test.local)
			}
			matched, err := automations.MatchPreparedScheduledTriggers(
				storedCronDefinition(t, test.expression), minute, location,
			)
			if err != nil {
				t.Fatal(err)
			}
			want := []automations.TriggerID{}
			if test.want {
				want = append(want, "scheduled")
			}
			if !slices.Equal(matched, want) {
				t.Fatalf("matched = %v, want %v", matched, want)
			}
		})
	}
}

// Preparation is owned by normalization and cannot silently match an edited rule.
func TestMatchPreparedScheduledTriggersRequiresUnchangedPreparation(t *testing.T) {
	t.Parallel()
	minute := time.Date(2026, time.October, 2, 7, 0, 0, 0, time.UTC)
	definition := cronDefinition(t, " 0  7 * * FRI ")
	if _, err := automations.MatchPreparedScheduledTriggers(definition, minute, time.UTC); !errors.Is(
		err, automations.ErrInvalidAutomation,
	) {
		t.Fatalf("unprepared definition error = %v", err)
	}
	normalized, err := automations.NormalizeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	cronBody := definition.Triggers[0].Body.(automations.CronTrigger)
	cronBody.Expression = "0 8 * * FRI"
	definition.Triggers[0].Body = cronBody
	matched, err := automations.MatchPreparedScheduledTriggers(normalized, minute, time.UTC)
	if err != nil || !slices.Equal(matched, []automations.TriggerID{"scheduled"}) {
		t.Fatalf("owned prepared match = %v, %v", matched, err)
	}
	if normalized.Triggers[0].Body.(automations.CronTrigger).Expression != "0 7 * * FRI" {
		t.Fatalf("normalized expression = %q", normalized.Triggers[0].Body.(automations.CronTrigger).Expression)
	}
	cronBody2 := normalized.Triggers[0].Body.(automations.CronTrigger)
	cronBody2.Expression = "0 8 * * FRI"
	normalized.Triggers[0].Body = cronBody2
	if _, err = automations.MatchPreparedScheduledTriggers(normalized, minute, time.UTC); !errors.Is(
		err, automations.ErrInvalidAutomation,
	) {
		t.Fatalf("edited preparation error = %v", err)
	}
	// The freely constructed boundary still prepares edited definitions afresh.
	matched, err = automations.ValidateAndMatchScheduledTriggers(normalized, minute, time.UTC)
	if err != nil || len(matched) != 0 {
		t.Fatalf("edited public match = %v, %v", matched, err)
	}
}

func TestValidateAndMatchScheduledTriggersGroupsInDefinitionOrderWithoutCrossProducts(t *testing.T) {
	t.Parallel()
	definition := validDomainDefinition(t)
	definition.Triggers = append(
		definition.Triggers,
		automations.Trigger{ID: "monday", Body: automations.CronTrigger{Expression: "0 7 * * MON"}},
		automations.Trigger{ID: "friday", Body: automations.CronTrigger{Expression: "0 9 * * FRI"}},
		automations.Trigger{ID: "quarter", Body: automations.CronTrigger{Expression: "*/15 * * * *"}},
	)
	for _, test := range []struct {
		utc  string
		want []automations.TriggerID
	}{
		{"2026-10-02T07:00:00Z", []automations.TriggerID{"quarter"}},
		{"2026-10-02T09:00:00Z", []automations.TriggerID{"friday", "quarter"}},
		{"2026-10-05T07:00:00Z", []automations.TriggerID{"monday", "quarter"}},
		{"2026-10-05T09:00:00Z", []automations.TriggerID{"quarter"}},
	} {
		minute, err := time.Parse(time.RFC3339, test.utc)
		if err != nil {
			t.Fatal(err)
		}
		matched, err := automations.ValidateAndMatchScheduledTriggers(definition, minute, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(matched, test.want) {
			t.Fatalf("%s: matched = %v, want %v", test.utc, matched, test.want)
		}
	}
}

func TestValidateAndMatchScheduledTriggersRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	minute := time.Date(2026, time.October, 2, 7, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		at       time.Time
		location *time.Location
		mutate   func(*automations.Definition)
	}{
		{"zero", time.Time{}, time.UTC, nil},
		{"seconds", minute.Add(time.Second), time.UTC, nil},
		{"nanoseconds", minute.Add(time.Nanosecond), time.UTC, nil},
		{"nil location", minute, nil, nil},
		{"expression", minute, time.UTC, func(d *automations.Definition) {
			cronBody := d.Triggers[0].Body.(automations.CronTrigger)
			cronBody.Expression = "? * * * *"
			d.Triggers[0].Body = cronBody
		}},
		{"definition", minute, time.UTC, func(d *automations.Definition) { d.Steps = nil }},
		{"family", minute, time.UTC, func(d *automations.Definition) { d.Triggers[0].Body = typedObservationTrigger(t) }},
		{"duplicate IDs", minute, time.UTC, func(d *automations.Definition) { d.Triggers = append(d.Triggers, d.Triggers[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := cronDefinition(t, "* * * * *")
			t.Parallel()
			if test.mutate != nil {
				test.mutate(&definition)
			}
			_, err := automations.ValidateAndMatchScheduledTriggers(definition, test.at, test.location)
			if !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("error = %v, want invalid automation", err)
			}
		})
	}
}

func TestCronTriggersNeverMatchDeviceFacts(t *testing.T) {
	t.Parallel()
	definition := cronDefinition(t, "* * * * *")
	entityID := newEntityID(t)
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := devices.NewEntityEventID()
	if err != nil {
		t.Fatal(err)
	}
	facts := []automations.DeviceFact{
		newObservationFact(t, entityID, modelTestTime),
		automations.EntityEventFact{
			FactID: factID, EventID: eventID, EntityID: entityID, Name: "press", EmittedAt: modelTestTime,
		},
	}
	for _, fact := range facts {
		matched, matchErr := automations.MatchTriggers(fact, definition)
		if matchErr != nil || len(matched) != 0 {
			t.Fatalf("family %s: matched = %v, error = %v", automations.FactFamily(fact), matched, matchErr)
		}
	}
}
