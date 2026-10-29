package sqlite

import "time"

func timestamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000000000Z") }
