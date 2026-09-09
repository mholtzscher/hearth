package automations

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/robfig/cron/v3"
)

// CronSchedule holds the pinned parser's masks; scheduling is owned by spec 2.
type CronSchedule struct{ schedule *cron.SpecSchedule }

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
