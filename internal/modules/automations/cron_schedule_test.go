package automations //nolint:testpackage // Tests inspect parser masks and inject repository clocks/failures.

import (
	"errors"
	"testing"
	"time"
)

func TestParseCronScheduleGrammar(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{"0 19 * * 1-5", " 0\t19 * JAN,Feb mon-FRI ", "*/5 0-23/2 1,15 * 0,6", "0 0 31 2 *", "0 0 */2 * MON", "0 0 1-31 * MON", "0 0 29 2 *", "0 0 * DEC SUN"} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			schedule, err := ParseCronSchedule(expression)
			if err != nil || schedule.schedule == nil {
				t.Fatalf("valid cron: %v", err)
			}
		})
	}
	for _, expression := range []string{"* * * *", "0 0 * *", "* * * * * *", "0 0 0 * * * 2026", "? * * * *", "0 0 L * *", "0 0 1W * *", "0 0 * * MON#2", "*/0 * * * *", "*/-1 * * * *", "0, * * * *", "0,,1 * * * *", "0 0 * * 5-1", "0 0 * * 7", "0 0 * * 0-7", "0 0 * * JANUARY", "@daily", "@monthly", "@every 1h", "TZ=UTC 0 0 * * *", "CRON_TZ=UTC 0 0 * * *", "60 * * * *", "0 24 * * *", "0 0 0 * *", "0 0 * 13 *", "0 0 * * SUNL", "*-5 * * * *"} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCronSchedule(expression)
			if !errors.Is(err, ErrInvalidAutomation) {
				t.Fatalf("invalid cron accepted: %v", err)
			}
		})
	}
}

// TestParseCronScheduleWildcardMarker pins the robfig v3.0.1 day-field dialect:
// "*" and "*/1" retain the wildcard marker while "*/2" clears it, and numeric
// full ranges are restricted fields rather than syntactic wildcards.
func TestParseCronScheduleWildcardMarker(t *testing.T) {
	t.Parallel()
	const cronStarBit = uint64(1) << 63

	starred, err := ParseCronSchedule("0 0 * * *")
	if err != nil {
		t.Fatalf("valid cron: %v", err)
	}
	if starred.schedule.Dom&cronStarBit == 0 || starred.schedule.Dow&cronStarBit == 0 {
		t.Fatalf("star must retain the wildcard marker: dom=%x dow=%x", starred.schedule.Dom, starred.schedule.Dow)
	}

	stepOne, err := ParseCronSchedule("0 0 */1 * *")
	if err != nil {
		t.Fatalf("valid cron: %v", err)
	}
	if stepOne.schedule.Dom&cronStarBit == 0 {
		t.Fatalf("*/1 must retain the wildcard marker: dom=%x", stepOne.schedule.Dom)
	}

	stepTwo, err := ParseCronSchedule("0 0 */2 * MON")
	if err != nil {
		t.Fatalf("valid cron: %v", err)
	}
	if stepTwo.schedule.Dom&cronStarBit != 0 {
		t.Fatalf("*/2 must clear the wildcard marker: dom=%x", stepTwo.schedule.Dom)
	}
	if stepTwo.schedule.Dom&(1<<1)|(stepTwo.schedule.Dom&(1<<3)) == 0 || stepTwo.schedule.Dom&(1<<2) != 0 {
		t.Fatalf("*/2 must select odd days of month: dom=%x", stepTwo.schedule.Dom)
	}

	fullRange, err := ParseCronSchedule("0 0 1-31 * MON")
	if err != nil {
		t.Fatalf("valid cron: %v", err)
	}
	if fullRange.schedule.Dom&cronStarBit != 0 {
		t.Fatalf("numeric full range must not carry the wildcard marker: dom=%x", fullRange.schedule.Dom)
	}
}

func mustParseCronScheduleForTest(t *testing.T, expression string) CronSchedule {
	t.Helper()
	schedule, err := ParseCronSchedule(expression)
	if err != nil {
		t.Fatalf("valid cron %q: %v", expression, err)
	}
	return schedule
}

func mustLoadCronTestLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %q: %v", name, err)
	}
	return location
}

func utcCronMinute(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
}

// TestMatchesCronMinuteCalendarSemantics protects household wall-clock cron
// field matching and fails if fields are evaluated in UTC, if the day-field
// OR/AND rule is inverted, or if impossible dates ever match.
func TestMatchesCronMinuteCalendarSemantics(t *testing.T) {
	t.Parallel()
	utc := time.UTC
	newYork := mustLoadCronTestLocation(t, "America/New_York")
	kiritimati := mustLoadCronTestLocation(t, "Pacific/Kiritimati")
	kathmandu := mustLoadCronTestLocation(t, "Asia/Kathmandu")

	for _, fixture := range []struct {
		name       string
		expression string
		minute     time.Time
		location   *time.Location
		want       bool
	}{
		// Weekday evenings follow household 19:00, not fixed UTC 19:00.
		{name: "weekday household evening matches", expression: "0 19 * * 1-5", minute: utcCronMinute(2026, time.June, 1, 23, 0), location: newYork, want: true},
		{name: "same UTC clock without household evening rejects", expression: "0 19 * * 1-5", minute: utcCronMinute(2026, time.June, 1, 19, 0), location: newYork, want: false},
		{name: "weekend household evening rejects", expression: "0 19 * * 1-5", minute: utcCronMinute(2026, time.June, 6, 23, 0), location: newYork, want: false},
		{name: "winter EST household evening matches", expression: "0 19 * * 1-5", minute: utcCronMinute(2026, time.January, 6, 0, 0), location: newYork, want: true},

		// Restricted day-of-month OR day-of-week.
		{name: "first that is Monday matches", expression: "0 0 1 * 1", minute: utcCronMinute(2026, time.June, 1, 0, 0), location: utc, want: true},
		{name: "first that is Tuesday matches", expression: "0 0 1 * 1", minute: utcCronMinute(2026, time.September, 1, 0, 0), location: utc, want: true},
		{name: "Monday that is not first matches", expression: "0 0 1 * 1", minute: utcCronMinute(2026, time.June, 8, 0, 0), location: utc, want: true},
		{name: "neither first nor Monday rejects", expression: "0 0 1 * 1", minute: utcCronMinute(2026, time.June, 10, 0, 0), location: utc, want: false},

		// Wildcard day-of-month requires the weekday test too.
		{name: "Monday matches Monday-only", expression: "0 0 * * 1", minute: utcCronMinute(2026, time.June, 1, 0, 0), location: utc, want: true},
		{name: "Tuesday rejects Monday-only", expression: "0 0 * * 1", minute: utcCronMinute(2026, time.June, 2, 0, 0), location: utc, want: false},
		{name: "first of month alone rejects Monday-only", expression: "0 0 * * 1", minute: utcCronMinute(2026, time.July, 1, 0, 0), location: utc, want: false},

		// Wildcard steps clear the wildcard marker, so odd days OR Monday.
		{name: "odd Wednesday matches step-two OR Monday", expression: "0 0 */2 * MON", minute: utcCronMinute(2026, time.June, 3, 0, 0), location: utc, want: true},
		{name: "even Monday matches step-two OR Monday", expression: "0 0 */2 * MON", minute: utcCronMinute(2026, time.June, 8, 0, 0), location: utc, want: true},
		{name: "even non-Monday rejects step-two OR Monday", expression: "0 0 */2 * MON", minute: utcCronMinute(2026, time.June, 4, 0, 0), location: utc, want: false},
		{name: "numeric full range matches every day", expression: "0 0 1-31 * MON", minute: utcCronMinute(2026, time.June, 10, 0, 0), location: utc, want: true},

		// February 31 is storable but never matches.
		{name: "impossible February date rejects common year", expression: "0 0 31 2 *", minute: utcCronMinute(2026, time.February, 28, 0, 0), location: utc, want: false},
		{name: "impossible February date rejects leap day", expression: "0 0 31 2 *", minute: utcCronMinute(2024, time.February, 29, 0, 0), location: utc, want: false},
		{name: "impossible February date rejects other month", expression: "0 0 31 2 *", minute: utcCronMinute(2026, time.January, 31, 0, 0), location: utc, want: false},
		{name: "impossible February date rejects March", expression: "0 0 31 2 *", minute: utcCronMinute(2026, time.March, 31, 0, 0), location: utc, want: false},

		// Leap day and month ends.
		{name: "leap day matches leap year", expression: "0 0 29 2 *", minute: utcCronMinute(2024, time.February, 29, 0, 0), location: utc, want: true},
		{name: "leap expression rejects common February", expression: "0 0 29 2 *", minute: utcCronMinute(2025, time.February, 28, 0, 0), location: utc, want: false},
		{name: "leap expression rejects February 28 in leap year", expression: "0 0 29 2 *", minute: utcCronMinute(2024, time.February, 28, 0, 0), location: utc, want: false},
		{name: "June 30 matches month end", expression: "0 9 30 6 *", minute: utcCronMinute(2026, time.June, 30, 9, 0), location: utc, want: true},
		{name: "June 29 rejects month end", expression: "0 9 30 6 *", minute: utcCronMinute(2026, time.June, 29, 9, 0), location: utc, want: false},
		{name: "January 31 matches month end", expression: "0 12 31 1 *", minute: utcCronMinute(2026, time.January, 31, 12, 0), location: utc, want: true},

		// Fixed-offset non-UTC IANA zones use household wall-clock fields.
		{name: "Kiritimati morning matches previous UTC evening", expression: "0 9 * * *", minute: utcCronMinute(2026, time.June, 1, 19, 0), location: kiritimati, want: true},
		{name: "Kiritimati night rejects same UTC morning", expression: "0 9 * * *", minute: utcCronMinute(2026, time.June, 1, 9, 0), location: kiritimati, want: false},
		{name: "Kathmandu half-hour offset matches", expression: "30 8 * * *", minute: utcCronMinute(2026, time.June, 1, 2, 45), location: kathmandu, want: true},
		{name: "Kathmandu nearby minute rejects", expression: "30 8 * * *", minute: utcCronMinute(2026, time.June, 1, 2, 30), location: kathmandu, want: false},

		// Sub-minute instants evaluate inside their containing UTC minute bucket.
		{name: "sub-minute instant matches containing bucket", expression: "0 19 * * *", minute: time.Date(2026, time.June, 1, 19, 0, 45, 0, time.UTC), location: utc, want: true},

		// Guards never match.
		{name: "nil location rejects", expression: "0 19 * * *", minute: utcCronMinute(2026, time.June, 1, 19, 0), location: nil, want: false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			schedule := mustParseCronScheduleForTest(t, fixture.expression)
			got := schedule.MatchesCronMinute(fixture.minute, fixture.location)
			if got != fixture.want {
				t.Fatalf(
					"MatchesCronMinute(%q at %v) = %v, want %v",
					fixture.expression,
					fixture.minute,
					got,
					fixture.want,
				)
			}
		})
	}

	t.Run("zero schedule rejects", func(t *testing.T) {
		t.Parallel()
		if (CronSchedule{}).MatchesCronMinute(utcCronMinute(2026, time.June, 1, 19, 0), utc) {
			t.Fatal("zero schedule must not match")
		}
	})
}

// TestMatchesCronMinuteSpringGap protects the spring-forward policy with the
// hand-specified specification oracle and fails if a nonexistent local minute
// ever matches.
func TestMatchesCronMinuteSpringGap(t *testing.T) {
	t.Parallel()
	newYork := mustLoadCronTestLocation(t, "America/New_York")
	schedule := mustParseCronScheduleForTest(t, "30 2 * * *")
	for _, instant := range []time.Time{
		utcCronMinute(2026, time.March, 8, 6, 30),
		utcCronMinute(2026, time.March, 8, 7, 0),
		utcCronMinute(2026, time.March, 8, 7, 30),
	} {
		if schedule.MatchesCronMinute(instant, newYork) {
			t.Fatalf("nonexistent 02:30 matched at %v", instant)
		}
	}
	day := time.Date(2026, time.March, 8, 0, 0, 0, 0, time.UTC)
	for offset := range 24 * 60 {
		instant := day.Add(time.Duration(offset) * time.Minute)
		if schedule.MatchesCronMinute(instant, newYork) {
			t.Fatalf("nonexistent 02:30 matched at %v", instant)
		}
	}

	existing := mustParseCronScheduleForTest(t, "30 1 * * *")
	if !existing.MatchesCronMinute(utcCronMinute(2026, time.March, 8, 6, 30), newYork) {
		t.Fatal("existing 01:30 EST must match on spring-forward day")
	}
}

// TestMatchesCronMinuteFallFold protects first-fold-only selection with the
// hand-specified New York oracle and fails if matching relies on local fields
// alone or on fired-before memory.
func TestMatchesCronMinuteFallFold(t *testing.T) {
	t.Parallel()
	newYork := mustLoadCronTestLocation(t, "America/New_York")
	schedule := mustParseCronScheduleForTest(t, "30 1 * * *")
	if !schedule.MatchesCronMinute(utcCronMinute(2026, time.November, 1, 5, 30), newYork) {
		t.Fatal("first 01:30 EDT must match")
	}
	if schedule.MatchesCronMinute(utcCronMinute(2026, time.November, 1, 6, 30), newYork) {
		t.Fatal("second 01:30 EST must not match")
	}
	if schedule.MatchesCronMinute(utcCronMinute(2026, time.November, 1, 4, 30), newYork) {
		t.Fatal("00:30 EDT must not match a 01:30 expression")
	}
	if schedule.MatchesCronMinute(utcCronMinute(2026, time.November, 1, 5, 0), newYork) {
		t.Fatal("01:00 EDT must not match a 01:30 expression")
	}
}

// TestMatchesCronMinuteFallFoldWildcards protects fold rejection when cron
// fields are wildcards and fails if the fold check depends on field matching.
func TestMatchesCronMinuteFallFoldWildcards(t *testing.T) {
	t.Parallel()
	newYork := mustLoadCronTestLocation(t, "America/New_York")
	schedule := mustParseCronScheduleForTest(t, "* 1 * * *")
	if !schedule.MatchesCronMinute(utcCronMinute(2026, time.November, 1, 5, 30), newYork) {
		t.Fatal("first 01:30 EDT must match a wildcard minute")
	}
	if schedule.MatchesCronMinute(utcCronMinute(2026, time.November, 1, 6, 30), newYork) {
		t.Fatal("second 01:30 EST must not match even with wildcard fields")
	}
}

// TestMatchesCronMinuteLordHoweFold protects first-fold-only selection for a
// 30-minute rollback with the hand-specified oracle and fails if the fold
// check assumes a one-hour offset change.
func TestMatchesCronMinuteLordHoweFold(t *testing.T) {
	t.Parallel()
	lordHowe := mustLoadCronTestLocation(t, "Australia/Lord_Howe")
	schedule := mustParseCronScheduleForTest(t, "45 1 * * *")
	if !schedule.MatchesCronMinute(utcCronMinute(2026, time.April, 4, 14, 45), lordHowe) {
		t.Fatal("first 01:45 +1100 must match")
	}
	if schedule.MatchesCronMinute(utcCronMinute(2026, time.April, 4, 15, 15), lordHowe) {
		t.Fatal("second 01:45 +1030 must not match")
	}
	if schedule.MatchesCronMinute(utcCronMinute(2026, time.April, 4, 14, 44), lordHowe) {
		t.Fatal("01:44 +1100 must not match a 01:45 expression")
	}
	if schedule.MatchesCronMinute(utcCronMinute(2026, time.April, 4, 15, 14), lordHowe) {
		t.Fatal("second 01:44 +1030 must not match a 01:45 expression")
	}
}
