package devices

import (
	"errors"
	"regexp"
	"unicode/utf8"
)

const maximumHealthReasonCodeLength = 128

var healthReasonCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)+$`)

// ValidateHealthReason is shared by report preparation and retained effective
// availability decoding; both accept freely constructed reason values.
func ValidateHealthReason(reason *HealthReason) error {
	if reason == nil || !utf8.ValidString(reason.Code) ||
		utf8.RuneCountInString(reason.Code) > maximumHealthReasonCodeLength ||
		!healthReasonCodePattern.MatchString(reason.Code) {
		return errors.New("health reason code must be a lowercase dotted identifier of at most 128 characters")
	}
	return nil
}
