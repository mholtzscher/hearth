package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// checkAssignedLabels verifies, inside the write transaction, that every
// assigned Label exists and has the kind its edit requires.
func checkAssignedLabels(
	ctx context.Context,
	queries *dbsqlc.Queries,
	area *devices.AreaEdit,
	tags *devices.TagEdit,
) error {
	want := make(map[devices.LabelID]devices.LabelKind)
	if area != nil && area.AreaID != nil {
		want[*area.AreaID] = devices.LabelKindArea
	}
	if tags != nil {
		for _, id := range tags.TagIDs {
			if kind, seen := want[id]; seen && kind != devices.LabelKindTag {
				return fmt.Errorf("%w: label %s cannot be both area and tag", devices.ErrInvalidMetadataPatch, id)
			}
			want[id] = devices.LabelKindTag
		}
	}
	if len(want) == 0 {
		return nil
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, string(id))
	}
	rows, err := queries.GetLabelKinds(ctx, dbsqlc.GetLabelKindsParams{Ids: ids})
	if err != nil {
		return fmt.Errorf("load assigned labels: %w", err)
	}
	found := make(map[devices.LabelID]string, len(rows))
	for _, row := range rows {
		found[devices.LabelID(row.ID)] = row.Kind
	}
	for _, id := range slices.Sorted(maps.Keys(want)) {
		kind, exists := found[id]
		if !exists {
			return fmt.Errorf("%w: %w: %s", devices.ErrInvalidMetadataPatch, devices.ErrLabelNotFound, id)
		}
		if devices.LabelKind(kind) != want[id] {
			return fmt.Errorf("%w: label %s is not %s", devices.ErrInvalidMetadataPatch, id, labelKindNoun(want[id]))
		}
	}
	return nil
}

func labelKindNoun(kind devices.LabelKind) string {
	if kind == devices.LabelKindArea {
		return "an area"
	}
	return "a tag"
}

// assignmentQueries are one resource's Area and tag statements, so Device and
// Entity metadata transactions share the change detection in applyLabelEdits.
type assignmentQueries struct {
	resource  string
	getArea   func(ctx context.Context, id string) (sql.NullString, error)
	setArea   func(ctx context.Context, id string, area sql.NullString, updatedAt string) error
	listTags  func(ctx context.Context, id string) ([]string, error)
	clearTags func(ctx context.Context, id string) error
	insertTag func(ctx context.Context, id, label string) error
	touch     func(ctx context.Context, id, updatedAt string) error
}

//nolint:dupl // Device and Entity assignments bind parallel generated statements.
func deviceAssignmentQueries(queries *dbsqlc.Queries) assignmentQueries {
	return assignmentQueries{
		resource: "device",
		getArea: func(ctx context.Context, id string) (sql.NullString, error) {
			return queries.GetDeviceAreaID(ctx, dbsqlc.GetDeviceAreaIDParams{ID: id})
		},
		setArea: func(ctx context.Context, id string, area sql.NullString, updatedAt string) error {
			return queries.UpdateDeviceArea(
				ctx,
				dbsqlc.UpdateDeviceAreaParams{ID: id, AreaID: area, UpdatedAt: updatedAt},
			)
		},
		listTags: func(ctx context.Context, id string) ([]string, error) {
			return queries.ListDeviceTagIDs(ctx, dbsqlc.ListDeviceTagIDsParams{DeviceID: id})
		},
		clearTags: func(ctx context.Context, id string) error {
			return queries.DeleteDeviceTags(ctx, dbsqlc.DeleteDeviceTagsParams{DeviceID: id})
		},
		insertTag: func(ctx context.Context, id, label string) error {
			return queries.InsertDeviceTag(ctx, dbsqlc.InsertDeviceTagParams{DeviceID: id, LabelID: label})
		},
		touch: func(ctx context.Context, id, updatedAt string) error {
			return queries.TouchDevice(ctx, dbsqlc.TouchDeviceParams{ID: id, UpdatedAt: updatedAt})
		},
	}
}

//nolint:dupl // Device and Entity assignments bind parallel generated statements.
func entityAssignmentQueries(queries *dbsqlc.Queries) assignmentQueries {
	return assignmentQueries{
		resource: "entity",
		getArea: func(ctx context.Context, id string) (sql.NullString, error) {
			return queries.GetEntityAreaID(ctx, dbsqlc.GetEntityAreaIDParams{ID: id})
		},
		setArea: func(ctx context.Context, id string, area sql.NullString, updatedAt string) error {
			return queries.UpdateEntityArea(
				ctx,
				dbsqlc.UpdateEntityAreaParams{ID: id, AreaID: area, UpdatedAt: updatedAt},
			)
		},
		listTags: func(ctx context.Context, id string) ([]string, error) {
			return queries.ListEntityTagIDs(ctx, dbsqlc.ListEntityTagIDsParams{EntityID: id})
		},
		clearTags: func(ctx context.Context, id string) error {
			return queries.DeleteEntityTags(ctx, dbsqlc.DeleteEntityTagsParams{EntityID: id})
		},
		insertTag: func(ctx context.Context, id, label string) error {
			return queries.InsertEntityTag(ctx, dbsqlc.InsertEntityTagParams{EntityID: id, LabelID: label})
		},
		touch: func(ctx context.Context, id, updatedAt string) error {
			return queries.TouchEntity(ctx, dbsqlc.TouchEntityParams{ID: id, UpdatedAt: updatedAt})
		},
	}
}

// applyLabelEdits checks the assigned Labels, then writes only what changed and
// reports whether anything did, so an unchanged edit preserves the update time.
func applyLabelEdits(
	ctx context.Context,
	queries *dbsqlc.Queries,
	statements assignmentQueries,
	id string,
	area *devices.AreaEdit,
	tags *devices.TagEdit,
	updatedAt time.Time,
) (bool, error) {
	if err := checkAssignedLabels(ctx, queries, area, tags); err != nil {
		return false, err
	}
	areaChanged, err := applyAreaEdit(ctx, statements, id, area, formatTime(updatedAt))
	if err != nil {
		return false, err
	}
	tagsChanged, err := applyTagEdit(ctx, statements, id, tags, formatTime(updatedAt))
	if err != nil {
		return false, err
	}
	return areaChanged || tagsChanged, nil
}

func applyAreaEdit(
	ctx context.Context,
	statements assignmentQueries,
	id string,
	edit *devices.AreaEdit,
	updatedAt string,
) (bool, error) {
	if edit == nil {
		return false, nil
	}
	current, err := statements.getArea(ctx, id)
	if err != nil {
		return false, fmt.Errorf("load %s area: %w", statements.resource, err)
	}
	next := sql.NullString{}
	if edit.AreaID != nil {
		next = sql.NullString{String: string(*edit.AreaID), Valid: true}
	}
	if current == next {
		return false, nil
	}
	if err = statements.setArea(ctx, id, next, updatedAt); err != nil {
		return false, fmt.Errorf("update %s area: %w", statements.resource, err)
	}
	return true, nil
}

func applyTagEdit(
	ctx context.Context,
	statements assignmentQueries,
	id string,
	edit *devices.TagEdit,
	updatedAt string,
) (bool, error) {
	if edit == nil {
		return false, nil
	}
	current, err := statements.listTags(ctx, id)
	if err != nil {
		return false, fmt.Errorf("load %s tags: %w", statements.resource, err)
	}
	next := make([]string, len(edit.TagIDs))
	for index, tag := range edit.TagIDs {
		next[index] = string(tag)
	}
	if slices.Equal(current, next) {
		return false, nil
	}
	if err = statements.clearTags(ctx, id); err != nil {
		return false, fmt.Errorf("clear %s tags: %w", statements.resource, err)
	}
	for _, tag := range next {
		if err = statements.insertTag(ctx, id, tag); err != nil {
			return false, fmt.Errorf("insert %s tag: %w", statements.resource, err)
		}
	}
	if err = statements.touch(ctx, id, updatedAt); err != nil {
		return false, fmt.Errorf("touch %s: %w", statements.resource, err)
	}
	return true, nil
}
