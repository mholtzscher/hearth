package automations

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"
)

const (
	cronExpressionMaxBytes = 512
	cronFieldCount         = 5
)

// The parser owns numeric bounds and weekday names; this only gates component syntax.
var cronComponentPattern = regexp.MustCompile(
	`^(\*|[0-9]+|[A-Za-z]{3}|([0-9]+|[A-Za-z]{3})-([0-9]+|[A-Za-z]{3}))(/[0-9]+)?$`,
)

func parseCronExpression(expression string) (string, *cron.SpecSchedule, error) {
	if len(expression) > cronExpressionMaxBytes || !utf8.ValidString(expression) {
		return "", nil, invalid("cron expression must be valid UTF-8 and at most 512 bytes")
	}
	fields := strings.Fields(expression)
	if len(fields) != cronFieldCount {
		return "", nil, invalid("cron expression requires exactly five fields")
	}
	if fields[2] != "*" || fields[3] != "*" {
		return "", nil, invalid("cron expression day-of-month and month must be literal *")
	}
	for _, index := range []int{0, 1, 4} {
		for component := range strings.SplitSeq(fields[index], ",") {
			if !cronComponentPattern.MatchString(component) {
				return "", nil, invalid("cron expression field %d has invalid component syntax", index+1)
			}
		}
	}
	normalized := strings.Join(fields, " ")
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := parser.Parse(normalized)
	if err != nil {
		return "", nil, fmt.Errorf("%w: cron expression: %w", ErrInvalidAutomation, err)
	}
	schedule, ok := parsed.(*cron.SpecSchedule)
	if !ok {
		return "", nil, invalid("cron expression did not produce a field schedule")
	}
	return normalized, schedule, nil
}

// MatchScheduledTriggers validates a freely constructed definition and selects
// matching cron IDs in declaration order. Eligibility is the repository's responsibility.
func MatchScheduledTriggers(
	definition Definition, minute time.Time, location *time.Location,
) ([]TriggerID, error) {
	if location == nil {
		return nil, invalid("schedule location is required")
	}
	if minute.IsZero() || !minute.Equal(minute.UTC().Truncate(time.Minute)) {
		return nil, invalid("schedule minute must be a nonzero UTC minute start")
	}
	prepared, schedules, err := normalizeAutomationDefinitionWithSchedules(definition)
	if err != nil {
		return nil, err
	}
	raw, err := EncodeDefinition(prepared)
	if err != nil {
		return nil, err
	}
	if len(raw) > automationDefinitionMaxBytes {
		return nil, invalid("schedule definition exceeds %d bytes", automationDefinitionMaxBytes)
	}
	local := minute.In(location)
	matched := make([]TriggerID, 0)
	if local.Second() != 0 {
		return matched, nil
	}
	for _, trigger := range prepared.Triggers {
		schedule := schedules[trigger.ID]
		if schedule != nil && schedule.Minute&(uint64(1)<<uint(local.Minute())) != 0 &&
			schedule.Hour&(uint64(1)<<uint(local.Hour())) != 0 &&
			schedule.Dow&(uint64(1)<<uint(local.Weekday())) != 0 {
			matched = append(matched, trigger.ID)
		}
	}
	return matched, nil
}
