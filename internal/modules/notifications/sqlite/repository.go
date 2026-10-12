// Package sqlite implements notification persistence and transactional runtime fencing.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite/dbsqlc"
)

type Repository struct {
	database *sql.DB
	queries  *dbsqlc.Queries
}

func NewRepository(database *sql.DB) *Repository {
	return &Repository{database: database, queries: dbsqlc.New(database)}
}

var _ notifications.Repository = (*Repository)(nil)

func (r *Repository) transaction(ctx context.Context, fn func(*dbsqlc.Queries) error) error {
	tx, err := r.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = fn(r.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) Register(ctx context.Context, in notifications.Registration) (notifications.Destination, error) {
	in, err := notifications.PrepareRegistration(in)
	if err != nil {
		return notifications.Destination{}, err
	}
	var destination notifications.Destination
	err = r.transaction(ctx, func(q *dbsqlc.Queries) error {
		_, err = q.ActiveRuntime(ctx, dbsqlc.ActiveRuntimeParams{AdapterID: in.AdapterID, RuntimeID: in.RuntimeID})
		if errors.Is(err, sql.ErrNoRows) {
			return notifications.ErrStaleRuntime
		}
		if err != nil {
			return err
		}
		id, mintErr := notifications.NewDestinationID()
		if mintErr != nil {
			return mintErr
		}
		if err = q.UpsertDestination(
			ctx,
			dbsqlc.UpsertDestinationParams{
				ID:        string(id),
				AdapterID: in.AdapterID,
				LocalKey:  in.Key,
				RuntimeID: in.RuntimeID,
				Name:      in.Name,
			},
		); err != nil {
			return err
		}
		row, readErr := q.GetDestinationByKey(
			ctx,
			dbsqlc.GetDestinationByKeyParams{AdapterID: in.AdapterID, LocalKey: in.Key},
		)
		if readErr != nil {
			return readErr
		}
		view, readErr := q.GetDestination(ctx, dbsqlc.GetDestinationParams{ID: row.ID})
		destination = notifications.Destination{
			ID:        notifications.DestinationID(view.ID),
			Name:      view.Name,
			Available: view.Available != 0,
		}
		return readErr
	})
	return destination, err
}

func (r *Repository) GetDestination(
	ctx context.Context,
	id notifications.DestinationID,
) (notifications.Destination, error) {
	if err := notifications.ValidateID(string(id), "ndst_"); err != nil {
		return notifications.Destination{}, err
	}
	row, err := r.queries.GetDestination(ctx, dbsqlc.GetDestinationParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		err = notifications.ErrDestinationNotFound
	}
	return notifications.Destination{
		ID:        notifications.DestinationID(row.ID),
		Name:      row.Name,
		Available: row.Available != 0,
	}, err
}

func (r *Repository) Admit(
	ctx context.Context,
	in notifications.SendInput,
	start, deadline time.Time,
) (notifications.Attempt, bool, error) {
	if err := notifications.ValidateSend(in); err != nil {
		return notifications.Attempt{}, false, err
	}
	if start.IsZero() || !deadline.Equal(start.Add(notifications.SendTimeout)) {
		return notifications.Attempt{}, false, notifications.ErrInvalidInput
	}
	var attempt notifications.Attempt
	var available bool
	err := r.transaction(ctx, func(q *dbsqlc.Queries) error {
		row, err := q.GetDestination(ctx, dbsqlc.GetDestinationParams{ID: string(in.DestinationID)})
		if errors.Is(err, sql.ErrNoRows) {
			return notifications.ErrDestinationNotFound
		}
		if err != nil {
			return err
		}
		attempt = notifications.Attempt{
			ID:              in.AttemptID,
			DestinationID:   in.DestinationID,
			DestinationName: row.Name,
			Route: notifications.RouteSnapshot{
				AdapterID: row.AdapterID,
				LocalKey:  row.LocalKey,
				RuntimeID: row.RuntimeID,
			},
			Message:   in.Message,
			StartedAt: start.UTC(),
			Deadline:  deadline.UTC(),
		}
		available = row.Available != 0
		return q.InsertAttempt(
			ctx,
			dbsqlc.InsertAttemptParams{
				ID:              string(in.AttemptID),
				DestinationID:   row.ID,
				DestinationName: row.Name,
				AdapterID:       row.AdapterID,
				LocalKey:        row.LocalKey,
				RuntimeID:       row.RuntimeID,
				Title:           in.Message.Title,
				Message:         in.Message.Text,
				StartedAt:       timestamp(start),
				Deadline:        timestamp(deadline),
			},
		)
	})
	// A reserved identity is not a deduplication key. Distinguish a collision
	// from ambiguous storage failure so consumers cannot adopt a prior attempt.
	type sqliteError interface{ Code() int }
	var databaseError sqliteError
	if errors.As(err, &databaseError) && databaseError.Code() == 1555 {
		return notifications.Attempt{}, false, notifications.ErrAttemptIDConflict
	}
	return attempt, available, err
}

func (r *Repository) Complete(
	ctx context.Context,
	id notifications.AttemptID,
	e notifications.Evidence,
	completed time.Time,
) (notifications.Attempt, error) {
	if notifications.ValidateID(string(id), "ntf_") != nil || notifications.ValidateEvidence(e) != nil ||
		completed.IsZero() {
		return notifications.Attempt{}, notifications.ErrInvalidInput
	}
	var attempt notifications.Attempt
	err := r.transaction(ctx, func(q *dbsqlc.Queries) error {
		_, err := q.CompleteAttempt(
			ctx,
			dbsqlc.CompleteAttemptParams{
				ID:          string(id),
				Outcome:     string(e.Outcome),
				ReasonCode:  e.ReasonCode,
				Receipt:     e.Receipt,
				CompletedAt: sql.NullString{String: timestamp(completed), Valid: true},
			},
		)
		if err != nil {
			return err
		}
		row, err := q.GetAttempt(ctx, dbsqlc.GetAttemptParams{ID: string(id)})
		if errors.Is(err, sql.ErrNoRows) {
			return notifications.ErrAttemptNotFound
		}
		if err != nil {
			return err
		}
		attempt, err = mapAttempt(row)
		return err
	})
	return attempt, err
}
func (r *Repository) GetAttempt(ctx context.Context, id notifications.AttemptID) (notifications.Attempt, error) {
	if err := notifications.ValidateID(string(id), "ntf_"); err != nil {
		return notifications.Attempt{}, err
	}
	row, err := r.queries.GetAttempt(ctx, dbsqlc.GetAttemptParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return notifications.Attempt{}, notifications.ErrAttemptNotFound
	}
	if err != nil {
		return notifications.Attempt{}, err
	}
	return mapAttempt(row)
}
func (r *Repository) Recover(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		return notifications.ErrInvalidInput
	}
	return r.queries.RecoverAttempts(
		ctx,
		dbsqlc.RecoverAttemptsParams{CompletedAt: sql.NullString{String: timestamp(at), Valid: true}},
	)
}
func (r *Repository) DeleteHistoryBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if cutoff.IsZero() || limit < 1 || limit > 500 {
		return 0, notifications.ErrInvalidInput
	}
	return r.queries.DeleteHistoryBefore(
		ctx,
		dbsqlc.DeleteHistoryBeforeParams{
			CompletedAt: sql.NullString{String: timestamp(cutoff), Valid: true},
			Limit:       int64(limit),
		},
	)
}
func timestamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func mapAttempt(row dbsqlc.NotificationAttempt) (notifications.Attempt, error) {
	a := notifications.Attempt{
		ID:              notifications.AttemptID(row.ID),
		DestinationID:   notifications.DestinationID(row.DestinationID),
		DestinationName: row.DestinationName,
		Route: notifications.RouteSnapshot{
			AdapterID: row.AdapterID,
			LocalKey:  row.LocalKey,
			RuntimeID: row.RuntimeID,
		},
		Message:    notifications.Message{Title: row.Title, Text: row.Message},
		Outcome:    notifications.Outcome(row.Outcome),
		ReasonCode: row.ReasonCode,
		Receipt:    row.Receipt,
	}
	var err error
	a.StartedAt, err = time.Parse(time.RFC3339Nano, row.StartedAt)
	if err != nil {
		return a, err
	}
	a.Deadline, err = time.Parse(time.RFC3339Nano, row.Deadline)
	if err != nil {
		return a, err
	}
	if row.CompletedAt.Valid {
		at, parseErr := time.Parse(time.RFC3339Nano, row.CompletedAt.String)
		if parseErr != nil {
			return a, parseErr
		}
		a.CompletedAt = &at
	}
	return a, nil
}

type cursor struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Time    string `json:"time"`
	Filter  string `json:"filter"`
}

func encodeCursor(c cursor) string {
	c.Version = 1
	payload, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(payload)
}
func decodeCursor(value, kind, filter string) (cursor, error) {
	if value == "" {
		return cursor{}, nil
	}
	var c cursor
	const maximumCursorSize = 2048
	if len(value) > maximumCursorSize {
		return c, notifications.ErrInvalidInput
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return c, notifications.ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || decoder.Decode(&struct{}{}) != io.EOF || c.Version != 1 || c.Kind != kind ||
		c.Filter != filter {
		return c, notifications.ErrInvalidInput
	}
	prefix := "ndst_"
	if kind == "attempt" {
		prefix = "ntf_"
		at, parseErr := time.Parse(time.RFC3339Nano, c.Time)
		if parseErr != nil || c.Time != timestamp(at) {
			return c, notifications.ErrInvalidInput
		}
	} else if c.Time != "" {
		return c, notifications.ErrInvalidInput
	}
	if err = notifications.ValidateID(c.ID, prefix); err != nil {
		return c, err
	}
	return c, nil
}

func (r *Repository) ListDestinations(
	ctx context.Context,
	limit int,
	value string,
) (notifications.DestinationPage, error) {
	page := notifications.DestinationPage{Items: []notifications.Destination{}}
	limit, err := notifications.PageLimit(limit)
	if err != nil {
		return page, err
	}
	c, err := decodeCursor(value, "destination", "")
	if err != nil {
		return page, err
	}
	rows, err := r.queries.ListDestinations(ctx, dbsqlc.ListDestinationsParams{ID: c.ID, Limit: int64(limit + 1)})
	if err != nil {
		return page, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = encodeCursor(cursor{Kind: "destination", ID: rows[limit-1].ID})
	}
	for _, row := range rows {
		page.Items = append(
			page.Items,
			notifications.Destination{
				ID:        notifications.DestinationID(row.ID),
				Name:      row.Name,
				Available: row.Available != 0,
			},
		)
	}
	return page, nil
}

func (r *Repository) ListAttempts(
	ctx context.Context,
	f notifications.AttemptFilter,
) (notifications.AttemptPage, error) {
	page := notifications.AttemptPage{Items: []notifications.Attempt{}}
	limit, err := notifications.PageLimit(f.Limit)
	if err != nil {
		return page, err
	}
	if f.DestinationID != "" {
		if err = notifications.ValidateID(string(f.DestinationID), "ndst_"); err != nil {
			return page, err
		}
	}
	c, err := decodeCursor(f.Cursor, "attempt", string(f.DestinationID))
	if err != nil {
		return page, err
	}
	rows, err := r.queries.ListAttempts(
		ctx,
		dbsqlc.ListAttemptsParams{
			DestinationID: string(f.DestinationID),
			BeforeTime:    c.Time,
			BeforeID:      c.ID,
			PageLimit:     int64(limit + 1),
		},
	)
	if err != nil {
		return page, fmt.Errorf("list notification attempts: %w", err)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		page.NextCursor = encodeCursor(
			cursor{Kind: "attempt", ID: last.ID, Time: last.StartedAt, Filter: string(f.DestinationID)},
		)
	}
	for _, row := range rows {
		a, mapErr := mapAttempt(row)
		if mapErr != nil {
			return page, mapErr
		}
		page.Items = append(page.Items, a)
	}
	return page, nil
}
