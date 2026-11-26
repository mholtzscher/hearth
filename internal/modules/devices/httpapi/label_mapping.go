package httpapi

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func labelBody(label devices.Label) LabelBody {
	return LabelBody{
		ID: string(label.ID), Name: label.Name, Kind: string(label.Kind),
		CreatedAt: formatTime(label.CreatedAt), UpdatedAt: formatTime(label.UpdatedAt),
	}
}

func labelRefBodies(refs []devices.LabelRef) []LabelRefBody {
	bodies := make([]LabelRefBody, len(refs))
	for index, ref := range refs {
		bodies[index] = LabelRefBody{ID: string(ref.ID), Name: ref.Name}
	}
	return bodies
}

func entityAreaBody(labels devices.EntityLabels) *EntityAreaBody {
	if labels.Area == nil {
		return nil
	}
	return &EntityAreaBody{
		ID: string(labels.Area.Label.ID), Name: labels.Area.Label.Name, Source: string(labels.Area.Source),
	}
}

func deviceAreaBody(labels devices.DeviceLabels) *DeviceAreaBody {
	if labels.Area == nil {
		return nil
	}
	return &DeviceAreaBody{ID: string(labels.Area.ID), Name: labels.Area.Name}
}

// labelError maps domain Label errors shared by the Label operations.
func labelError(err error) error {
	switch {
	case errors.Is(err, devices.ErrInvalidLabel):
		return huma.NewError(http.StatusBadRequest, "invalid label")
	case errors.Is(err, devices.ErrLabelNotFound):
		return huma.NewError(http.StatusNotFound, "label not found")
	case errors.Is(err, devices.ErrLabelNameConflict):
		return labelConflictProblem("a label with this name already exists", "label_name_conflict")
	case errors.Is(err, devices.ErrLabelLimitReached):
		return labelConflictProblem("the household label limit has been reached", "label_limit_reached")
	default:
		return nil
	}
}
