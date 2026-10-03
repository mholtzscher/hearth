package automations

import "time"

// CronTrigger matches a restricted five-field household-local clock rule.
type CronTrigger struct {
	Expression string
}

// ScheduleTick samples one current minute for admission, not public history evidence.
type ScheduleTick struct {
	At       time.Time
	Location *time.Location
}
