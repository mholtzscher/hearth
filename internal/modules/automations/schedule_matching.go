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

// cronSchedule retains only immutable clock fields, not cron library types.
type cronSchedule struct {
	expression string
	minute     uint64
	hour       uint64
	weekday    uint64
}

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

// ValidateAndMatchScheduledTriggers validates and prepares a freely constructed
// definition, including its encoded size, then selects matching cron IDs in
// declaration order. Eligibility is the repository's responsibility.
func ValidateAndMatchScheduledTriggers(
	definition Definition, minute time.Time, location *time.Location,
) ([]TriggerID, error) {
	if err := validateScheduleMinute(minute, location); err != nil {
		return nil, err
	}
	prepared, err := prepareDefinition(definition)
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
	return matchPreparedScheduledTriggers(prepared, minute, location)
}

func validateScheduleMinute(minute time.Time, location *time.Location) error {
	if location == nil {
		return invalid("schedule location is required")
	}
	if minute.IsZero() || !minute.Equal(minute.UTC().Truncate(time.Minute)) {
		return invalid("schedule minute must be a nonzero UTC minute start")
	}
	return nil
}

// MatchPreparedScheduledTriggers consumes an unchanged definition returned by
// DecodeDefinition, NormalizeDefinition, or NormalizeAndEncodeDefinition, reusing
// its compiled clock fields. It checks minute/location and preparation integrity,
// not structural validity or encoded size. Freely constructed or edited
// definitions use ValidateAndMatchScheduledTriggers.
func MatchPreparedScheduledTriggers(
	definition Definition, minute time.Time, location *time.Location,
) ([]TriggerID, error) {
	if err := validateScheduleMinute(minute, location); err != nil {
		return nil, err
	}
	return matchPreparedScheduledTriggers(definition, minute, location)
}

func matchPreparedScheduledTriggers(
	definition Definition, minute time.Time, location *time.Location,
) ([]TriggerID, error) {
	local := minute.In(location)
	matched := make([]TriggerID, 0)
	for _, trigger := range definition.Triggers {
		var schedule *cronSchedule
		switch body := trigger.Body.(type) {
		case CronTrigger:
			if body.schedule == nil || body.schedule.expression != body.Expression {
				return nil, invalid("trigger %q schedule must be normalized before matching", trigger.ID)
			}
			schedule = body.schedule
		case ObservationTrigger, EntityEventTrigger, HeldStateTrigger:
			continue
		default:
			return nil, invalid("trigger %q has unsupported body", trigger.ID)
		}
		if local.Second() == 0 && schedule.minute&(uint64(1)<<uint(local.Minute())) != 0 &&
			schedule.hour&(uint64(1)<<uint(local.Hour())) != 0 &&
			schedule.weekday&(uint64(1)<<uint(local.Weekday())) != 0 {
			matched = append(matched, trigger.ID)
		}
	}
	return matched, nil
}
