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

You receive a digest of the household right now. Write exactly 6 starter questions that:
- the assistant can answer from what the digest lists. It can read Entity state and history,
  explain why an Automation failed or was skipped, chart readings over time, build dashboard
  views, and draft Automations for the person to review and save.
- never ask the assistant to change a device. Do not write requests to turn on or off, lock,
  unlock, open, close, set, start, stop, play, or adjust anything.
- name real Entities, Devices, and Automations from the digest when they are about something
  specific.
- mix current problems (unavailable Entities, Automation failures), current conditions, and one
  general question.
- differ from the previous starter questions when the digest lists them.

The title is the button text: a short question or a phrase starting with "Show", at most 60
characters. The prompt is the full message, at most 240 characters.

Answer with JSON only, no prose and no code fence:
{"starter_questions":[{"title":"...","prompt":"..."}]}`

// Digest bounds keep one pass to a few thousand tokens however large the
// household is.
const (
	maxDigestUnavailable = 20
	maxDigestReadings    = 80
	maxDigestEntityBytes = 400
	maxDigestAutomations = 15
	maxDigestBytes       = 16 << 10
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

const digestPowerType = "hearth.power/v1"

// renderHouseholdDigest renders the user message for one pass. Sections are
// ordered by usefulness so that the byte limit drops the least useful lines.
func renderHouseholdDigest(digest HouseholdDigest, previews []string, previous []StarterQuestion) string {
	var lines []string
	lines = append(lines,
		"Local time: "+digest.LocalTime.Format("Monday 2006-01-02 15:04")+" ("+partOfDay(digest.LocalTime.Hour())+")",
		"Unit system: "+string(digest.UnitSystem),
	)
	lines = append(lines, digestUnavailableLines(digest.Entities)...)
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

func digestUnavailableLines(entities []DigestEntity) []string {
	var unavailable []DigestEntity
	for _, entity := range entities {
		if entity.Availability == DigestUnavailable {
			unavailable = append(unavailable, entity)
		}
	}
	if len(unavailable) == 0 {
		return []string{"", "Unavailable Entities: none"}
	}
	lines := []string{"", fmt.Sprintf("Unavailable Entities (%d):", len(unavailable))}
	for index, entity := range unavailable {
		if index == maxDigestUnavailable {
			lines = append(lines, fmt.Sprintf("- and %d more", len(unavailable)-maxDigestUnavailable))
			break
		}
		lines = append(lines, truncateBytes("- "+entityLabel(entity), maxDigestEntityBytes))
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
		line := fmt.Sprintf("- %s: %d", entityType, counts[entityType])
		if entityType == digestPowerType {
			line += fmt.Sprintf(" (%d on)", powerOn)
		}
		lines = append(lines, line)
	}
	return lines
}

func entityLabel(entity DigestEntity) string {
	label := entity.Name + " [" + entity.Type + "]"
	if entity.Device != "" && entity.Device != entity.Name {
		label += " on " + entity.Device
	}
	return label
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
