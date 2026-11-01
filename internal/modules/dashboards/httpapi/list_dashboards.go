package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"

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
		},
		h.listDashboards,
	)
}

type listInput struct {
	Limit  int    `query:"limit"  default:"50"`
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
	p := dashboards.ListParams{Limit: in.Limit}
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
		return nil, mapError(err)
	}
	out := &listOutput{}
	out.Body.Items = page.Items
	if page.HasMore && len(page.Items) > 0 {
		raw, _ := json.Marshal(cursor{Version: 1, Resource: "dashboards", ID: page.Items[len(page.Items)-1].ID})
		out.Body.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
