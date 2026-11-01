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
		return dashboards.Dashboard{}, fmt.Errorf("%w: stored definition: %w", dashboards.ErrCorruptDashboard, err)
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

func dashboardSummaryFromRow(row dbsqlc.Dashboard) (dashboards.Summary, error) {
	d, err := dashboardFromRow(row)
	if err != nil {
		return dashboards.Summary{}, err
	}
	var envelope struct {
		Title string `json:"title"`
	}
	if decodeErr := json.Unmarshal(d.Definition, &envelope); decodeErr != nil {
		return dashboards.Summary{}, decodeErr
	}
	return dashboards.Summary{
		ID:        d.ID,
		Title:     envelope.Title,
		Revision:  d.Revision,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}, nil
}
