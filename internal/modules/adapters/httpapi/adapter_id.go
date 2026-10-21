package httpapi

import (
	"regexp"
)

var adapterIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func validAdapterID(value string) bool {
	return adapterIDPattern.MatchString(value)
}
