package sqlite_test

import (
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func isStoredCorruption(err error) bool {
	var diagnostic interface{ Cause() error }
	return errors.As(err, &diagnostic) && diagnostic.Cause() != nil &&
		!errors.Is(err, automations.ErrInvalidAutomation) &&
		!errors.Is(err, automations.ErrInvalidDeviceFact)
}
