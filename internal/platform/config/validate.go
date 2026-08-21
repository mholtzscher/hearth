package config

import (
	"fmt"
	"net/url"
	"regexp"
	"unicode/utf8"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func ValidateSlug(field, value string) error {
	if !slugPattern.MatchString(value) {
		return fmt.Errorf("%s must match %s", field, slugPattern.String())
	}
	return nil
}

func ValidateName(field, value string) error {
	length := utf8.RuneCountInString(value)
	if length < 1 || length > 128 {
		return fmt.Errorf("%s must contain 1-128 characters", field)
	}
	return nil
}

func ValidateExternalID(field, value string) error {
	length := utf8.RuneCountInString(value)
	if length < 1 || length > 256 {
		return fmt.Errorf("%s must contain 1-256 characters", field)
	}
	return nil
}

func ValidateNATSURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("nats_url is invalid: %w", err)
	}
	if parsed.Scheme != "nats" || parsed.Host == "" {
		return fmt.Errorf("nats_url must be an absolute nats:// URL")
	}
	return nil
}
