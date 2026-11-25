package sqlite

import (
	"context"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// labelFromRow decodes one stored catalog row, rejecting values the schema
// should have prevented.
func labelFromRow(row dbsqlc.Label) (devices.Label, error) {
	id, err := devices.ParseLabelID(row.ID)
	if err != nil {
		return devices.Label{}, fmt.Errorf("decode label ID: %w", err)
	}
	kind, err := devices.ParseLabelKind(row.Kind)
	if err != nil {
		return devices.Label{}, fmt.Errorf("decode label %s kind: %w", row.ID, err)
	}
	createdAt, err := parseTime(row.CreatedAt)
	if err != nil {
		return devices.Label{}, fmt.Errorf("decode label %s created_at: %w", row.ID, err)
	}
	updatedAt, err := parseTime(row.UpdatedAt)
	if err != nil {
		return devices.Label{}, fmt.Errorf("decode label %s updated_at: %w", row.ID, err)
	}
	return devices.Label{ID: id, Name: row.Name, Kind: kind, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

// attachEntityLabels sets the effective Labels of every view from
// entity_effective_labels, the only definition of the override and union rule.
// Every repository read that returns an Entity calls it.
func attachEntityLabels(ctx context.Context, queries *dbsqlc.Queries, views []devices.EntityWithState) error {
	if len(views) == 0 {
		return nil
	}
	ids := make([]string, len(views))
	byID := make(map[devices.EntityID]*devices.EntityLabels, len(views))
	for index := range views {
		ids[index] = string(views[index].Entity.ID)
		views[index].Labels = devices.EntityLabels{Tags: []devices.LabelRef{}}
		byID[views[index].Entity.ID] = &views[index].Labels
	}
	rows, err := queries.ListEffectiveEntityLabels(ctx, dbsqlc.ListEffectiveEntityLabelsParams{EntityIds: ids})
	if err != nil {
		return fmt.Errorf("list effective entity labels: %w", err)
	}
	for _, row := range rows {
		labels := byID[devices.EntityID(row.EntityID)]
		if labels == nil {
			return fmt.Errorf("effective label row names unrequested entity %s", row.EntityID)
		}
		ref := devices.LabelRef{ID: devices.LabelID(row.LabelID), Name: row.Name}
		switch row.Kind {
		case string(devices.LabelKindArea):
			source := devices.AreaSource(row.Source)
			if source != devices.AreaSourceDevice && source != devices.AreaSourceEntity {
				return fmt.Errorf("entity %s area has unknown source %q", row.EntityID, row.Source)
			}
			labels.Area = &devices.EffectiveArea{Label: ref, Source: source}
		case string(devices.LabelKindTag):
			// Rows are ordered by name and ID, so a tag held by both the Entity
			// and its Device arrives as adjacent duplicates.
			if last := len(labels.Tags) - 1; last >= 0 && labels.Tags[last].ID == ref.ID {
				continue
			}
			labels.Tags = append(labels.Tags, ref)
		default:
			return fmt.Errorf("entity %s has label of unknown kind %q", row.EntityID, row.Kind)
		}
	}
	return nil
}

func attachEntityLabel(
	ctx context.Context,
	queries *dbsqlc.Queries,
	view devices.EntityWithState,
) (devices.EntityWithState, error) {
	views := []devices.EntityWithState{view}
	if err := attachEntityLabels(ctx, queries, views); err != nil {
		return devices.EntityWithState{}, err
	}
	return views[0], nil
}

// attachDeviceLabels sets the Labels assigned directly to every Device. Every
// repository read that returns a Device calls it.
func attachDeviceLabels(ctx context.Context, queries *dbsqlc.Queries, items []devices.Device) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	byID := make(map[devices.DeviceID]*devices.DeviceLabels, len(items))
	for index := range items {
		ids[index] = string(items[index].ID)
		items[index].Labels = devices.DeviceLabels{Tags: []devices.LabelRef{}}
		byID[items[index].ID] = &items[index].Labels
	}
	areas, err := queries.ListDeviceAreas(ctx, dbsqlc.ListDeviceAreasParams{DeviceIds: ids})
	if err != nil {
		return fmt.Errorf("list device areas: %w", err)
	}
	for _, row := range areas {
		labels := byID[devices.DeviceID(row.DeviceID)]
		if labels == nil {
			return fmt.Errorf("device area row names unrequested device %s", row.DeviceID)
		}
		labels.Area = &devices.LabelRef{ID: devices.LabelID(row.LabelID), Name: row.Name}
	}
	tags, err := queries.ListDeviceTags(ctx, dbsqlc.ListDeviceTagsParams{DeviceIds: ids})
	if err != nil {
		return fmt.Errorf("list device tags: %w", err)
	}
	for _, row := range tags {
		labels := byID[devices.DeviceID(row.DeviceID)]
		if labels == nil {
			return fmt.Errorf("device tag row names unrequested device %s", row.DeviceID)
		}
		labels.Tags = append(labels.Tags, devices.LabelRef{ID: devices.LabelID(row.LabelID), Name: row.Name})
	}
	return nil
}

func attachDeviceLabel(ctx context.Context, queries *dbsqlc.Queries, device devices.Device) (devices.Device, error) {
	items := []devices.Device{device}
	if err := attachDeviceLabels(ctx, queries, items); err != nil {
		return devices.Device{}, err
	}
	return items[0], nil
}
