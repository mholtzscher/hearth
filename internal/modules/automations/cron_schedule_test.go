package automations //nolint:testpackage // Tests inspect parser masks and inject repository clocks/failures.

import (
	"errors"
	"testing"
)

func TestParseCronScheduleGrammar(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{"0 19 * * 1-5", " 0\t19 * JAN,Feb mon-FRI ", "*/5 0-23/2 1,15 * 0,6", "0 0 31 2 *", "0 0 */2 * MON", "0 0 1-31 * MON"} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			schedule, err := ParseCronSchedule(expression)
			if err != nil || schedule.schedule == nil {
				t.Fatalf("valid cron: %v", err)
			}
		})
	}
	for _, expression := range []string{"* * * *", "* * * * * *", "0 0 0 * * * 2026", "? * * * *", "0 0 L * *", "0 0 1W * *", "0 0 * * MON#2", "*/0 * * * *", "*/-1 * * * *", "0, * * * *", "0,,1 * * * *", "0 0 * * 5-1", "0 0 * * 7", "@daily", "@every 1h", "TZ=UTC 0 0 * * *", "CRON_TZ=UTC 0 0 * * *", "60 * * * *", "0 24 * * *", "0 0 0 * *", "0 0 * 13 *", "0 0 * * SUNL", "*-5 * * * *"} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCronSchedule(expression)
			if !errors.Is(err, ErrInvalidAutomation) {
				t.Fatalf("invalid cron accepted: %v", err)
			}
		})
	}
}
