package mcpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func labelDetail(label devices.Label) LabelDetail {
	return LabelDetail{LabelID: string(label.ID), Name: label.Name, Kind: string(label.Kind)}
}

func deviceArea(area *devices.LabelRef) *LabelSummary {
	if area == nil {
		return nil
	}
	return &LabelSummary{LabelID: string(area.ID), Name: area.Name}
}

func entityArea(area *devices.EffectiveArea) *EntityArea {
	if area == nil {
		return nil
	}
	return &EntityArea{
		LabelID: string(area.Label.ID), Name: area.Label.Name, Source: string(area.Source),
	}
}

// labelSummaries is never nil: a target without tags reports an empty array.
func labelSummaries(refs []devices.LabelRef) []LabelSummary {
	result := make([]LabelSummary, len(refs))
	for index, ref := range refs {
		result[index] = LabelSummary{LabelID: string(ref.ID), Name: ref.Name}
	}
	return result
}

// labelPatchEdits converts decoded Label arguments to domain edits. An omitted
// area_id stays nil; an explicit null clears the Area. Omitted tag_ids stay nil.
func labelPatchEdits(areaPresent bool, area *labelID, tags []labelID) (*devices.AreaEdit, *devices.TagEdit) {
	var areaEdit *devices.AreaEdit
	if areaPresent {
		areaEdit = &devices.AreaEdit{}
		if area != nil {
			id := devices.LabelID(*area)
			areaEdit.AreaID = &id
		}
	}
	var tagEdit *devices.TagEdit
	if tags != nil {
		tagEdit = &devices.TagEdit{TagIDs: labelIDs(tags)}
	}
	return areaEdit, tagEdit
}
