package httpapi

import (
	"encoding/json"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

type dashboardOutput struct{ Body dashboards.Dashboard }

func mapError(err error) error {
	switch {
	case errors.Is(err, dashboards.ErrInvalidDefinition):
		return huma.Error400BadRequest("invalid dashboard input")
	case errors.Is(err, dashboards.ErrNotFound):
		return huma.Error404NotFound("dashboard not found")
	case errors.Is(err, dashboards.ErrRevisionConflict):
		return huma.Error409Conflict("dashboard revision conflict")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
func parseBody(raw json.RawMessage, revision bool) (dashboards.Definition, int64, error) {
	if len(raw) > 64*1024 {
		return dashboards.Definition{}, 0, huma.Error413RequestEntityTooLarge("request body too large")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return dashboards.Definition{}, 0, huma.Error400BadRequest("invalid request")
	}
	if len(obj) != 1 && (!revision || len(obj) != 2) {
		return dashboards.Definition{}, 0, huma.Error400BadRequest("invalid request")
	}
	if revision && len(obj) != 2 {
		return dashboards.Definition{}, 0, huma.Error400BadRequest("expected revision required")
	}
	d, err := dashboards.ParseDefinition(obj["definition"])
	if err != nil {
		return dashboards.Definition{}, 0, mapError(err)
	}
	var rev int64
	if revision {
		if revisionErr := json.Unmarshal(obj["expected_revision"], &rev); revisionErr != nil || rev < 1 {
			return dashboards.Definition{}, 0, huma.Error400BadRequest("invalid revision")
		}
	}
	return d, rev, nil
}
