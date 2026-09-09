package automations

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// CronSchedule is a parsed five-field household calendar rule.
// Store compiled masks privately; do not expose the dependency's types.
type CronSchedule struct{ schedule *cron.SpecSchedule }

// cronWildcardMarker preserves the pinned parser's wildcard marker: robfig
// cron v3.0.1 sets the top bit when a star participates in a field, keeps it
// for "*" and "*/1", and clears it for steps greater than one. Numeric full
// ranges never set it, so they count as restricted day fields.
const cronWildcardMarker = uint64(1) << 63

// cronFoldLookbackMinutes bounds the first fold backward comparison. It covers
// the difference between offsets within plus or minus 24 hours in supported
// IANA tzdata, including date-line and half-hour rollbacks.
const cronFoldLookbackMinutes = 2880

var cronFieldSyntax = regexp.MustCompile(`^(\*|[0-9]+|[A-Za-z]{3})(-([0-9]+|[A-Za-z]{3}))?(/[0-9]+)?$`)

// ParseCronSchedule accepts exactly the five-field household cron grammar.
func ParseCronSchedule(expression string) (CronSchedule, error) {
	fields := strings.Fields(expression)
	const cronFieldCount = 5
	if len(fields) != cronFieldCount {
		return CronSchedule{}, fmt.Errorf("%w: cron requires five fields", ErrInvalidAutomation)
	}
	for index, field := range fields {
		for part := range strings.SplitSeq(field, ",") {
			if !cronFieldSyntax.MatchString(part) || strings.HasPrefix(part, "*-") {
				return CronSchedule{}, fmt.Errorf("%w: cron field %d syntax", ErrInvalidAutomation, index+1)
			}
		}
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := parser.Parse(strings.Join(fields, " "))
	if err != nil {
		return CronSchedule{}, fmt.Errorf("%w: cron field range or increment", ErrInvalidAutomation)
	}
	schedule, ok := parsed.(*cron.SpecSchedule)
	if !ok {
		return CronSchedule{}, fmt.Errorf("%w: cron calendar required", ErrInvalidAutomation)
	}
	return CronSchedule{schedule: schedule}, nil
}

// MatchesCronMinute only matches the first occurrence of a repeated local minute.
// minute must be a whole UTC minute, and location is the household timezone.
//
// Sub-minute instants evaluate inside their containing UTC minute bucket. A
// repeated local minute during a daylight saving fold matches only at its
// first chronological occurrence, even if the process starts during the second
// one; the fold check is independent of cron field matching.
func (s CronSchedule) MatchesCronMinute(minute time.Time, location *time.Location) bool {
	if s.schedule == nil || location == nil {
		return false
	}
	utcMinute := minute.UTC().Truncate(time.Minute)
	if !cronFirstFoldUTCInstant(utcMinute, location) {
		return false
	}
	return s.matchesCronLocalWallClock(utcMinute.In(location))
}

// matchesCronLocalWallClock reports whether the compiled cron field masks match
// one household wall-clock instant. The local instant must already be expressed
// in the household timezone. Other fields always combine with AND; day matching
// follows the traditional rule where restricted day-of-month OR day-of-week
// match, while either wildcard side requires both field tests to pass.
//
// C2 calculates cronFirstFoldUTCInstant once per household minute and reuses
// this cron field matching across every definition evaluated at that minute.
func (s CronSchedule) matchesCronLocalWallClock(local time.Time) bool {
	schedule := s.schedule
	if schedule == nil {
		return false
	}
	if uint64(1)<<uint(local.Minute())&schedule.Minute == 0 {
		return false
	}
	if uint64(1)<<uint(local.Hour())&schedule.Hour == 0 {
		return false
	}
	if uint64(1)<<uint(local.Month())&schedule.Month == 0 {
		return false
	}
	dayOfMonthMatch := uint64(1)<<uint(local.Day())&schedule.Dom != 0
	dayOfWeekMatch := uint64(1)<<uint(local.Weekday())&schedule.Dow != 0
	if schedule.Dom&cronWildcardMarker != 0 || schedule.Dow&cronWildcardMarker != 0 {
		return dayOfMonthMatch && dayOfWeekMatch
	}
	return dayOfMonthMatch || dayOfWeekMatch
}

// matchScheduledTriggers parses every Trigger expression and collects the matching
// Trigger snapshots in definition array order for one household minute. The local
// instant must already be expressed in the household timezone, and firstFold must
// be the single cronFirstFoldUTCInstant result computed once for that minute.
// A nonmatching minute returns empty subsets, never an error; an unparsable
// stored expression aborts the minute visibly instead of dispatching partially.
func matchScheduledTriggers(
	triggers []AutomationTrigger,
	local time.Time,
	firstFold bool,
) ([]AutomationTrigger, []AutomationTriggerID, error) {
	matched := []AutomationTrigger{}
	ids := []AutomationTriggerID{}
	for _, trigger := range triggers {
		schedule, err := ParseCronSchedule(trigger.Expression)
		if err != nil {
			return nil, nil, err
		}
		if firstFold && schedule.matchesCronLocalWallClock(local) {
			matched = append(matched, trigger)
			ids = append(ids, trigger.ID)
		}
	}
	return matched, ids, nil
}

// chronological occurrence of its local calendar minute. It compares the local
// year, month, day, hour, and minute fields at the candidate against each of
// the preceding cronFoldLookbackMinutes UTC minute buckets; an earlier bucket
// with identical local fields means the candidate is the second fold of a
// repeated local minute during a daylight saving rollback and is rejected.
//
// The check keeps the first fold policy independent of cron field matching and
// of any fired-before memory, so C2 calculates it once per household minute
// and reuses the result across every cron field matching decision at that
// minute. Nonexistent local minutes need no special case: UTC minute iteration
// naturally never visits them.
func cronFirstFoldUTCInstant(utcMinute time.Time, location *time.Location) bool {
	candidate := utcMinute.In(location)
	candidateYear, candidateMonth, candidateDay := candidate.Date()
	candidateHour, candidateMinute, _ := candidate.Clock()
	for lookback := 1; lookback <= cronFoldLookbackMinutes; lookback++ {
		earlier := utcMinute.Add(-time.Duration(lookback) * time.Minute).In(location)
		earlierYear, earlierMonth, earlierDay := earlier.Date()
		earlierHour, earlierMinute, _ := earlier.Clock()
		if earlierYear == candidateYear &&
			earlierMonth == candidateMonth &&
			earlierDay == candidateDay &&
			earlierHour == candidateHour &&
			earlierMinute == candidateMinute {
			return false
		}
	}
	return true
}
