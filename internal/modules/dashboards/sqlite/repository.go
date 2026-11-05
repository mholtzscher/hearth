// Package sqlite stores dashboards independently of conversations.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/sqlite/dbsqlc"
)

type Repository struct {
	db *sql.DB
	q  *dbsqlc.Queries
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db, q: dbsqlc.New(db)} }

var _ dashboards.Repository = (*Repository)(nil)

func (r *Repository) CreateDashboard(ctx context.Context, d dashboards.Definition) (dashboards.Dashboard, error) {
	if err := dashboards.ValidateDefinition(d); err != nil {
		return dashboards.Dashboard{}, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	key := "dash_" + id.String()
	if err = r.q.CreateDashboard(
		ctx,
		dbsqlc.CreateDashboardParams{ID: key, DefinitionJson: string(raw), CreatedAt: now, UpdatedAt: now},
	); err != nil {
		return dashboards.Dashboard{}, err
	}
	return r.GetDashboard(ctx, dashboards.DashboardID(key))
}
func (r *Repository) GetDashboard(ctx context.Context, id dashboards.DashboardID) (dashboards.Dashboard, error) {
	if _, err := dashboards.ParseDashboardID(string(id)); err != nil {
		return dashboards.Dashboard{}, fmt.Errorf("%w: id", dashboards.ErrInvalidDefinition)
	}
	row, err := r.q.GetDashboard(ctx, dbsqlc.GetDashboardParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return dashboards.Dashboard{}, dashboards.ErrNotFound
	}
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	return dashboardFromRow(row)
}

func (r *Repository) ListDashboards(
	ctx context.Context,
	p dashboards.ListParams,
) (dashboards.Page[dashboards.Summary], error) {
	if p.Limit == 0 {
		p.Limit = 50
	}
	if p.Limit < 1 || p.Limit > 200 {
		return dashboards.Page[dashboards.Summary]{}, fmt.Errorf("%w: limit", dashboards.ErrInvalidDefinition)
	}
	after := ""
	if p.AfterID != nil {
		if _, err := dashboards.ParseDashboardID(string(*p.AfterID)); err != nil {
			return dashboards.Page[dashboards.Summary]{}, fmt.Errorf("%w: cursor", dashboards.ErrInvalidDefinition)
		}
		after = string(*p.AfterID)
	}
	rows, err := r.q.ListDashboards(ctx, dbsqlc.ListDashboardsParams{ID: after, Limit: int64(p.Limit + 1)})
	if err != nil {
		return dashboards.Page[dashboards.Summary]{}, err
	}
	page := dashboards.Page[dashboards.Summary]{
		Items:   make([]dashboards.Summary, 0, p.Limit),
		HasMore: len(rows) > p.Limit,
	}
	if page.HasMore {
		rows = rows[:p.Limit]
	}
	for _, row := range rows {
		summary, mapErr := dashboardSummaryFromRow(row)
		if mapErr != nil {
			return page, mapErr
		}
		page.Items = append(page.Items, summary)
	}
	return page, nil
}

func (r *Repository) SaveDashboard(
	ctx context.Context,
	p dashboards.SaveDashboardParams,
) (dashboards.Dashboard, error) {
	if _, err := dashboards.ParseDashboardID(string(p.ID)); err != nil || p.ExpectedRevision < 1 {
		return dashboards.Dashboard{}, fmt.Errorf("%w: id or revision", dashboards.ErrInvalidDefinition)
	}
	if err := dashboards.ValidateDefinition(p.Definition); err != nil {
		return dashboards.Dashboard{}, err
	}
	raw, err := json.Marshal(p.Definition)
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := r.q.WithTx(tx)
	row, err := q.ReplaceDashboard(
		ctx,
		dbsqlc.ReplaceDashboardParams{
			ID:             string(p.ID),
			Revision:       p.ExpectedRevision,
			DefinitionJson: string(raw),
			UpdatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return dashboards.Dashboard{}, missingOrConflict(ctx, q, p.ID)
	}
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	result, err := dashboardFromRow(row)
	if err != nil {
		return dashboards.Dashboard{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return dashboards.Dashboard{}, commitErr
	}
	return result, nil
}
func (r *Repository) DeleteDashboard(ctx context.Context, id dashboards.DashboardID, revision int64) error {
	if _, err := dashboards.ParseDashboardID(string(id)); err != nil || revision < 1 {
		return fmt.Errorf("%w: id or revision", dashboards.ErrInvalidDefinition)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := r.q.WithTx(tx)
	count, err := q.DeleteDashboard(ctx, dbsqlc.DeleteDashboardParams{ID: string(id), Revision: revision})
	if err != nil {
		return err
	}
	if count == 0 {
		return missingOrConflict(ctx, q, id)
	}
	return tx.Commit()
}
func missingOrConflict(ctx context.Context, q *dbsqlc.Queries, id dashboards.DashboardID) error {
	_, err := q.GetDashboard(ctx, dbsqlc.GetDashboardParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return dashboards.ErrNotFound
	}
	if err != nil {
		return err
	}
	return dashboards.ErrRevisionConflict
}
