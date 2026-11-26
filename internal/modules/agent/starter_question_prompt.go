package agent

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// starterQuestionInstruction is the system message for one generation pass.
const starterQuestionInstruction = `You write starter questions for Hearth, a home-automation assistant.
A household member sees them as buttons in an empty conversation. Tapping one sends its prompt
to the assistant immediately.

You receive a digest of the household right now. Write exactly 6 starter questions that a person
living in this home would actually ask:
- Write in everyday words, the way people talk about their home: "Is it warmer upstairs than in
  the office?" or "Did anything go wrong overnight?". Never use the words entity or digest, and
  never repeat type names, model numbers, or ID suffixes. Shorten technical names to what a
  person would say, such as "the office air" for "SEN55 Temperature on Apollo AIR-1 8945e4".
- Fit the time of day and what the digest shows: comfort, weather, doors and locks, what happened
  overnight or while people were out, and how things changed today.
- Ask about a problem only when the digest lists an offline Device or an Automation problem, and
  write at most one such question.
- Ask only what the assistant can answer from the digest. It can read current state and history,
  explain why an Automation failed or was skipped, chart readings over time, build dashboard
  views, and draft Automations for the person to review and save. One question may suggest an
  Automation or dashboard view that would suit this home.
- Never ask the assistant to change anything. Do not write requests to turn on or off, lock,
  unlock, open, close, set, start, stop, play, or adjust anything.
- Differ from the previous starter questions when the digest lists them.

The title is the button text: a short, natural question or a request starting with "Show me", at
most 60 characters. The prompt is the full message in the person's own voice, at most 240
characters.

Answer with JSON only, no prose and no code fence:
{"starter_questions":[{"title":"...","prompt":"..."}]}`

// Digest bounds keep one pass to a few thousand tokens however large the
// household is.
const (
	maxDigestOfflineDevices = 20
	maxDigestReadings       = 80
	maxDigestEntityBytes    = 400
	maxDigestAutomations    = 15
	maxDigestBytes          = 16 << 10
)

// isDigestReadingType reports whether the digest lists an Entity type's State.
// Other types appear only as counts.
func isDigestReadingType(entityType string) bool {
	switch entityType {
	case "hearth.lock/v1", "hearth.cover/v1", "hearth.binarysensor/v1", "hearth.thermostat/v1",
		"hearth.temperature/v1", "hearth.relativehumidity/v1", "hearth.mediaplayer/v1":
		return true
	default:
		return false
	}
}

const (
	digestPowerType = "hearth.power/v1"
	// digestSensorKind is the Device kind of sensors, buttons, and remotes.
	digestSensorKind = "sensor"
)

// renderHouseholdDigest renders the user message for one pass. Sections are
// ordered by usefulness so that the byte limit drops the least useful lines.
func renderHouseholdDigest(digest HouseholdDigest, previews []string, previous []StarterQuestion) string {
	var lines []string
	lines = append(lines,
		"Local time: "+digest.LocalTime.Format("Monday 2006-01-02 15:04")+" ("+partOfDay(digest.LocalTime.Hour())+")",
		"Unit system: "+string(digest.UnitSystem),
	)
	lines = append(lines, digestOfflineDeviceLines(digest.Entities)...)
	lines = append(lines, digestAutomationLines(digest.Automations)...)
	lines = append(lines, digestReadingLines(digest.Entities)...)
	lines = append(lines, digestCountLines(digest.Entities)...)
	if len(previews) > 0 {
		lines = append(lines, "", "Recent conversations, newest first:")
		for _, preview := range previews {
			lines = append(lines, "- "+preview)
		}
	}
	if len(previous) > 0 {
		lines = append(lines, "", "Previous starter questions:")
		for _, question := range previous {
			lines = append(lines, "- "+question.Title)
		}
	}
	return joinWithinBytes(lines, maxDigestBytes)
}

// Hours at which each part of the day starts, in household local time.
const (
	morningStartHour   = 5
	afternoonStartHour = 12
	eveningStartHour   = 17
	nightStartHour     = 22
)

func partOfDay(hour int) string {
	switch {
	case hour < morningStartHour:
		return "night"
	case hour < afternoonStartHour:
		return "morning"
	case hour < eveningStartHour:
		return "afternoon"
	case hour < nightStartHour:
		return "evening"
	default:
		return "night"
	}
}

// digestOfflineDeviceLines lists the Devices worth asking about because none
// of their Entities is available. A reachable Device with one unavailable
// Entity is usually in a mode that does not report it, and battery sensors and
// buttons are routinely unavailable between uses, so neither is listed.
func digestOfflineDeviceLines(entities []DigestEntity) []string {
	type deviceAvailability struct {
		label     string
		kind      string
		available bool
	}
	var order []string
	byID := make(map[string]*deviceAvailability)
	for _, entity := range entities {
		device, seen := byID[entity.DeviceID]
		if !seen {
			device = &deviceAvailability{label: entity.Device + " (" + entity.DeviceKind + ")", kind: entity.DeviceKind}
			byID[entity.DeviceID] = device
			order = append(order, entity.DeviceID)
		}
		device.available = device.available || entity.Availability == DigestAvailable
	}
	var offline []string
	for _, id := range order {
		device := byID[id]
		if !device.available && device.kind != "" && device.kind != digestSensorKind {
			offline = append(offline, device.label)
		}
	}
	if len(offline) == 0 {
		return []string{"", "Offline Devices: none"}
	}
	lines := []string{"", fmt.Sprintf("Offline Devices (%d):", len(offline))}
	for index, label := range offline {
		if index == maxDigestOfflineDevices {
			lines = append(lines, fmt.Sprintf("- and %d more", len(offline)-maxDigestOfflineDevices))
			break
		}
		lines = append(lines, truncateBytes("- "+label, maxDigestEntityBytes))
	}
	return lines
}

func digestAutomationLines(automations []DigestAutomation) []string {
	enabled := 0
	var troubled []DigestAutomation
	for _, automation := range automations {
		if automation.Enabled {
			enabled++
		}
		if len(automation.Problems) > 0 {
			troubled = append(troubled, automation)
		}
	}
	lines := []string{"", fmt.Sprintf("Automations: %d enabled, %d disabled", enabled, len(automations)-enabled)}
	if len(troubled) == 0 {
		return append(lines, "Automation problems in the last 24 hours: none")
	}
	slices.SortStableFunc(troubled, func(left, right DigestAutomation) int {
		return right.Problems[0].At.Compare(left.Problems[0].At)
	})
	lines = append(lines, "Automation problems in the last 24 hours, newest first:")
	for index, automation := range troubled {
		if index == maxDigestAutomations {
			lines = append(lines, fmt.Sprintf("- and %d more Automations", len(troubled)-maxDigestAutomations))
			break
		}
		problems := make([]string, 0, len(automation.Problems))
		for _, problem := range automation.Problems {
			problems = append(problems, problem.Kind+" at "+problem.At.Format("Mon 15:04"))
		}
		line := "- " + automation.Name
		if !automation.Enabled {
			line += " (disabled)"
		}
		lines = append(lines, truncateBytes(line+": "+strings.Join(problems, "; "), maxDigestEntityBytes))
	}
	return lines
}

func digestReadingLines(entities []DigestEntity) []string {
	var readings []DigestEntity
	for _, entity := range entities {
		if entity.Availability == DigestAvailable && entity.State != nil && isDigestReadingType(entity.Type) {
			readings = append(readings, entity)
		}
	}
	if len(readings) == 0 {
		return nil
	}
	slices.SortStableFunc(readings, func(left, right DigestEntity) int {
		return cmp.Compare(left.Type, right.Type)
	})
	lines := []string{"", "Current readings:"}
	for index, entity := range readings {
		if index == maxDigestReadings {
			lines = append(lines, fmt.Sprintf("- and %d more", len(readings)-maxDigestReadings))
			break
		}
		line := "- " + entityLabel(entity) + ": " + string(entity.State)
		if units := unitLabels(entity.Units); units != "" {
			line += " (" + units + ")"
		}
		lines = append(lines, truncateBytes(line, maxDigestEntityBytes))
	}
	return lines
}

func digestCountLines(entities []DigestEntity) []string {
	counts := make(map[string]int)
	powerOn := 0
	for _, entity := range entities {
		counts[entity.Type]++
		if entity.Type == digestPowerType && string(entity.State) == "true" {
			powerOn++
		}
	}
	if len(counts) == 0 {
		return []string{"", "Entities: none"}
	}
	types := make([]string, 0, len(counts))
	for entityType := range counts {
		types = append(types, entityType)
	}
	slices.Sort(types)
	lines := []string{"", "Entity counts by type:"}
	for _, entityType := range types {
		line := fmt.Sprintf("- %s: %d", typeWords(entityType), counts[entityType])
		if entityType == digestPowerType {
			line += fmt.Sprintf(" (%d on)", powerOn)
		}
		lines = append(lines, line)
	}
	return lines
}

func entityLabel(entity DigestEntity) string {
	label := entity.Name + " (" + typeWords(entity.Type) + ")"
	if entity.Device != "" && entity.Device != entity.Name {
		label += " on " + entity.Device
	}
	if entity.Area != "" {
		label += " in " + entity.Area
	}
	return label
}

// typeWords names an Entity type in plain words, such as "humidity" for
// hearth.relativehumidity/v1, so the model has no type IDs to repeat.
func typeWords(entityType string) string {
	name := strings.TrimPrefix(entityType, "hearth.")
	name, _, _ = strings.Cut(name, "/")
	switch name {
	case "binarysensor":
		return "binary sensor"
	case "colorhs", "colorrgb", "colorxy":
		return "color"
	case "colormode":
		return "color mode"
	case "colortemp":
		return "color temperature"
	case "dailyforecast":
		return "daily forecast"
	case "enumaction":
		return "action"
	case "enumevent":
		return "button event"
	case "enumsetting", "numericsetting":
		return "setting"
	case "hourlyforecast":
		return "hourly forecast"
	case "mediaplayer":
		return "media player"
	case "numericsensor":
		return "measurement"
	case "relativehumidity":
		return "humidity"
	case "textsensor":
		return "status text"
	default:
		return name
	}
}

func unitLabels(units map[string]string) string {
	if len(units) == 0 {
		return ""
	}
	fields := make([]string, 0, len(units))
	for field := range units {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	labels := make([]string, 0, len(fields))
	for _, field := range fields {
		name := field
		if name == "" {
			name = "value"
		}
		labels = append(labels, name+" in "+units[field])
	}
	return strings.Join(labels, ", ")
}

// truncateBytes cuts text to at most limit bytes on a rune boundary and marks
// the cut.
func truncateBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const marker = " …(cut)"
	cut := limit - len(marker)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + marker
}

// joinWithinBytes joins lines with newlines, dropping lines that would exceed
// limit and noting how many were dropped.
func joinWithinBytes(lines []string, limit int) string {
	const reserve = 64
	var builder strings.Builder
	dropped := 0
	for _, line := range lines {
		if dropped > 0 || builder.Len()+len(line)+1 > limit-reserve {
			dropped++
			continue
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	if dropped > 0 {
		fmt.Fprintf(&builder, "(%d more lines omitted)\n", dropped)
	}
	return builder.String()
}
