package sqlite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/sqlite/dbsqlc"
)

func dashboardFromRow(row dbsqlc.Dashboard) (dashboards.Dashboard, error) {
	id, err := dashboards.ParseDashboardID(row.ID)
	if err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("stored dashboard id: %w", err)
	}
	raw, err := dashboards.ReadDefinition(json.RawMessage(row.DefinitionJson))
	if err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("stored dashboard definition: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	updated, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	return dashboards.Dashboard{
		ID:         id,
		Revision:   row.Revision,
		Definition: raw,
		CreatedAt:  created,
		UpdatedAt:  updated,
	}, nil
}
