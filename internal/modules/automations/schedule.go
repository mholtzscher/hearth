package automations

import "time"

// CronTrigger matches a restricted five-field household-local clock rule.
type CronTrigger struct {
	Expression string
	// schedule is immutable preparation owned by normalization. It is never
	// encoded and must not be reused after changing Expression.
	schedule *cronSchedule
}

// ScheduleTick samples one current minute for admission, not public history evidence.
type ScheduleTick struct {
	At       time.Time
	Location *time.Location
}
