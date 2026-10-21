package httpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func healthReasonBody(reason *devices.HealthReason) *HealthReasonBody {
	if reason == nil {
		return nil
	}
	return &HealthReasonBody{Code: reason.Code}
}
