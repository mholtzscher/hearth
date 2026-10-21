package mcpapi

import (
	"regexp"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

var adapterIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func validAdapterID(value string) bool  { return adapterIDPattern.MatchString(value) }
func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

type HealthSummary struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code,omitempty"`
}

func healthSummary(health adapters.AdapterHealth) HealthSummary {
	result := HealthSummary{Status: string(health.Status)}
	if health.Reason != nil {
		result.ReasonCode = health.Reason.Code
	}
	return result
}
