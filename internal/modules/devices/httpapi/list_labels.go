package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerListLabels(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-labels", Method: http.MethodGet, Path: "/labels",
		Summary: "List household Labels", Tags: []string{labelsTag},
		Description: "Lists every Area and tag, ordered by kind, then name.",
		Errors:      []int{http.StatusInternalServerError},
	}, handler.listLabels)
}

type ListLabelsOutput struct {
	Body LabelCollectionBody
}

type LabelCollectionBody struct {
	Items []LabelBody `json:"items" nullable:"false"`
}

func (handler *handler) listLabels(ctx context.Context, _ *struct{}) (*ListLabelsOutput, error) {
	labels, err := handler.devices.ListLabels(ctx)
	if err != nil {
		return nil, handler.internalAPIError(ctx, "list-labels", "service", err)
	}
	body := LabelCollectionBody{Items: make([]LabelBody, len(labels))}
	for index, label := range labels {
		body.Items[index] = labelBody(label)
	}
	return &ListLabelsOutput{Body: body}, nil
}
