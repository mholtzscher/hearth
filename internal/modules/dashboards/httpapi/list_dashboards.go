package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func registerListDashboards(api huma.API, h *handler) {
	huma.Register(
		api,
		huma.Operation{
			OperationID: "list-dashboards",
			Method:      http.MethodGet,
			Path:        "/dashboards",
			Summary:     "List dashboards",
			Parameters: []*huma.Param{
				{
					Name: "limit",
					In:   "query",
					Schema: &huma.Schema{
						Type:    "integer",
						Default: defaultPageLimit,
						Minimum: new(float64(1)),
						Maximum: new(float64(maxPageLimit)),
					},
				},
			},
			Errors: []int{http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusInternalServerError},
		},
		h.listDashboards,
	)
}

type listInput struct {
	Limit  string `query:"limit"`
	Cursor string `query:"cursor"`
}
type listOutput struct {
	Body struct {
		Items      []dashboards.Summary `json:"items"`
		NextCursor string               `json:"next_cursor,omitempty"`
	}
}
type cursor struct {
	Version  int                    `json:"v"`
	Resource string                 `json:"resource"`
	ID       dashboards.DashboardID `json:"id"`
}

func (h *handler) listDashboards(ctx context.Context, in *listInput) (*listOutput, error) {
	p := dashboards.ListParams{}
	if in.Limit != "" {
		limit, parseErr := strconv.Atoi(in.Limit)
		if parseErr != nil || limit < 1 || limit > maxPageLimit {
			return nil, huma.Error400BadRequest("invalid limit")
		}
		p.Limit = limit
	}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		var c cursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Resource != "dashboards" {
			return nil, huma.Error400BadRequest("invalid cursor")
		}
		p.AfterID = &c.ID
	}
	page, err := h.service.ListDashboards(ctx, p)
	if err != nil {
		return nil, h.mapError(ctx, "list-dashboards", err)
	}
	out := &listOutput{}
	out.Body.Items = page.Items
	if page.HasMore && len(page.Items) > 0 {
		raw, _ := json.Marshal(cursor{Version: 1, Resource: "dashboards", ID: page.Items[len(page.Items)-1].ID})
		out.Body.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
