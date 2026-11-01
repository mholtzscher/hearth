package httpapi

import (
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

type dashboardOutput struct{ Body dashboards.Dashboard }

func (h *handler) parseBody(
	ctx context.Context,
	operation string,
	raw json.RawMessage,
	revision bool,
) (dashboards.Definition, int64, error) {
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
		return dashboards.Definition{}, 0, h.mapError(ctx, operation, err)
	}
	var rev int64
	if revision {
		if revisionErr := json.Unmarshal(obj["expected_revision"], &rev); revisionErr != nil || rev < 1 {
			return dashboards.Definition{}, 0, huma.Error400BadRequest("invalid revision")
		}
	}
	return d, rev, nil
}
