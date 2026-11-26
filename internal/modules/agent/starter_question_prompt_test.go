package agent //nolint:testpackage // Tests exercise package-private digest rendering.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/household"
)

func digestTime() time.Time {
	return time.Date(2026, 11, 24, 19, 42, 0, 0, time.FixedZone("CST", -6*60*60))
}

func reading(name, entityType, state string) DigestEntity {
	return DigestEntity{
		Name: name, Device: name + " device", Type: entityType,
		Availability: DigestAvailable, State: json.RawMessage(state),
	}
}

func TestRenderHouseholdDigestListsSectionsInOrder(t *testing.T) {
	t.Parallel()
	temperature := reading("Office Temperature", "hearth.temperature/v1", `21.5`)
	temperature.Units = map[string]string{"": "°F"}
	temperature.Area = "Office"
	digest := HouseholdDigest{
		LocalTime:  digestTime(),
		UnitSystem: household.UnitSystemUS,
		Entities: []DigestEntity{
			temperature,
			{
				Name: "Garage Lock", DeviceID: "dev_garage", Device: "Garage", DeviceKind: "lock",
				Type: "hearth.lock/v1", Availability: DigestUnavailable,
			},
			reading("Hall Light", "hearth.power/v1", `true`),
			reading("Desk Lamp", "hearth.power/v1", `false`),
			reading("Hourly Forecast", "hearth.hourlyforecast/v1", `{"secret_forecast":1}`),
		},
		Automations: []DigestAutomation{
			{Name: "Porch lights at sunset", Enabled: true, Problems: []DigestAutomationProblem{
				{At: digestTime().Add(-time.Hour), Kind: DigestProblemFailed},
			}},
			{Name: "Vacation mode", Enabled: false},
		},
	}
	got := renderHouseholdDigest(
		digest,
		[]string{"Why is the garage cold?"},
		[]StarterQuestion{question("Show humidity today")},
	)
	wantInOrder := []string{
		"Local time: Tuesday 2026-11-24 19:42 (evening)",
		"Unit system: us",
		"Offline Devices (1):",
		"- Garage (lock)",
		"Automations: 1 enabled, 1 disabled",
		"- Porch lights at sunset: failed at Tue 18:42",
		"Current readings:",
		"- Office Temperature (temperature) on Office Temperature device in Office: 21.5 (value in °F)",
		"- power: 2 (1 on)",
		"- Why is the garage cold?",
		"- Show humidity today",
	}
	position := 0
	for _, want := range wantInOrder {
		index := strings.Index(got[position:], want)
		if index < 0 {
			t.Fatalf("digest lacks %q after offset %d:\n%s", want, position, got)
		}
		position += index + len(want)
	}
	if strings.Contains(got, "secret_forecast") {
		t.Fatalf("digest includes forecast State:\n%s", got)
	}
	if strings.Contains(got, "Hall Light") {
		t.Fatalf("digest lists a power Entity instead of counting it:\n%s", got)
	}
}

func TestRenderHouseholdDigestListsOnlyUnreachableControlledDevices(t *testing.T) {
	t.Parallel()
	entity := func(deviceID, device, kind, name, availability string) DigestEntity {
		return DigestEntity{
			Name: name, DeviceID: deviceID, Device: device, DeviceKind: kind,
			Type: "hearth.power/v1", Availability: availability,
		}
	}
	got := renderHouseholdDigest(HouseholdDigest{
		LocalTime: digestTime(),
		Entities: []DigestEntity{
			entity("dev_fan", "Office Fan", "relay", "Power", DigestUnavailable),
			entity("dev_button", "Office Fan Button", "sensor", "Action", DigestUnavailable),
			entity("dev_button", "Office Fan Button", "sensor", "Battery", DigestUnavailable),
			entity("dev_purifier", "Basement Air Purifier", "air-purifier", "Manual fan level", DigestUnavailable),
			entity("dev_purifier", "Basement Air Purifier", "air-purifier", "Power", DigestAvailable),
			entity("dev_unknown", "Mystery", "", "Power", DigestUnavailable),
		},
	}, nil, nil)
	if !strings.Contains(got, "Offline Devices (1):\n- Office Fan (relay)\n") {
		t.Fatalf("digest lacks the unreachable relay alone:\n%s", got)
	}
	for _, unwanted := range []string{"Office Fan Button", "Manual fan level", "Basement Air Purifier", "Mystery"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("digest lists %q as a problem:\n%s", unwanted, got)
		}
	}
}

func TestRenderHouseholdDigestRespectsBounds(t *testing.T) {
	t.Parallel()
	digest := HouseholdDigest{LocalTime: digestTime(), UnitSystem: household.UnitSystemMetric}
	for index := range maxDigestOfflineDevices + 5 {
		name := fmt.Sprintf("Offline %02d", index)
		digest.Entities = append(digest.Entities, DigestEntity{
			Name: name, DeviceID: name, Device: name, DeviceKind: "relay",
			Type: "hearth.power/v1", Availability: DigestUnavailable,
		})
	}
	digest.Entities = append(
		digest.Entities,
		reading("Big Sensor", "hearth.binarysensor/v1", `"`+strings.Repeat("x", 1000)+`"`),
	)
	for index := range maxDigestReadings + 7 {
		digest.Entities = append(
			digest.Entities,
			reading(fmt.Sprintf("Contact %03d", index), "hearth.binarysensor/v1", `false`),
		)
	}
	for index := range maxDigestAutomations + 3 {
		digest.Automations = append(digest.Automations, DigestAutomation{
			Name:    fmt.Sprintf("Automation %02d", index),
			Enabled: true,
			Problems: []DigestAutomationProblem{
				{At: digestTime().Add(-time.Duration(index) * time.Minute), Kind: DigestProblemInterrupted},
			},
		})
	}
	got := renderHouseholdDigest(digest, nil, nil)
	for _, want := range []string{"- and 5 more\n", "- and 3 more Automations\n", "Automation 00:", "…(cut)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Offline 20") || strings.Contains(got, "Automation 15") {
		t.Fatalf("digest exceeds a section cap:\n%s", got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if len(line) > maxDigestEntityBytes {
			t.Fatalf("line has %d bytes: %q", len(line), line)
		}
	}
	readings := strings.Count(got, " (binary sensor) on ")
	if readings != maxDigestReadings {
		t.Fatalf("listed %d readings, want %d", readings, maxDigestReadings)
	}
}

func TestRenderHouseholdDigestStaysWithinByteLimit(t *testing.T) {
	t.Parallel()
	previews := make([]string, 0, 2000)
	for index := range 2000 {
		previews = append(previews, fmt.Sprintf("Question number %04d about the household", index))
	}
	got := renderHouseholdDigest(HouseholdDigest{LocalTime: digestTime()}, previews, nil)
	if len(got) > maxDigestBytes {
		t.Fatalf("digest has %d bytes, limit %d", len(got), maxDigestBytes)
	}
	if !strings.Contains(got, "more lines omitted") {
		t.Fatalf("digest does not report dropped lines")
	}
}

func TestStartsWithCommandVerbIgnoresQuestions(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]bool{
		"Is the front door locked?":   false,
		"Show the lights that are on": false,
		"Which lights are on?":        false,
		"Settings for the thermostat": false,
		"turn on the lights":          true,
		"Open the garage":             true,
		"Turn\noff the lamp":          true,
		"Lock\tthe door":              true,
		"  ":                          false,
	} {
		if got := startsWithCommandVerb(text); got != want {
			t.Fatalf("startsWithCommandVerb(%q) = %v, want %v", text, got, want)
		}
	}
}
