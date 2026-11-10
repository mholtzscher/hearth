// Package httpapi exposes read-only household settings.
package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/household"
)

// Register publishes GET /household.
func Register(api huma.API, settings household.Settings) {
	unitSystem := settings.UnitSystem
	if unitSystem == "" {
		unitSystem = household.UnitSystemMetric
	}
	body := householdBody{Timezone: settings.Timezone, UnitSystem: string(unitSystem)}
	huma.Register(
		api,
		huma.Operation{
			OperationID: "get-household",
			Method:      http.MethodGet,
			Path:        "/household",
			Summary:     "Get household settings",
			Description: "Household-wide settings fixed by Core configuration. Core uses unit_system for numeric read responses; canonical stored State is unchanged.",
			Errors:      []int{http.StatusInternalServerError},
		},
		func(context.Context, *struct{}) (*householdOutput, error) {
			return &householdOutput{Body: body}, nil
		},
	)
}

type householdOutput struct {
	Body householdBody
}

type householdBody struct {
	Timezone   string `json:"timezone"    doc:"IANA timezone name"`
	UnitSystem string `json:"unit_system" doc:"Display unit system" enum:"metric,us"`
}
