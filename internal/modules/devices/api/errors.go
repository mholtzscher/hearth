package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func apiError(status int, message string) error {
	return huma.NewError(status, message)
}

type disabledCommandProblem struct {
	Type      string `json:"type"       format:"uri" default:"about:blank"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	CommandID string `json:"command_id"`
}

func (problem *disabledCommandProblem) Error() string  { return problem.Detail }
func (problem *disabledCommandProblem) GetStatus() int { return problem.Status }
func (problem *disabledCommandProblem) ContentType(contentType string) string {
	if contentType == "application/json" {
		return "application/problem+json"
	}
	return contentType
}

func entityDisabledProblem(commandID devices.CommandID) error {
	return &disabledCommandProblem{
		Type: "about:blank", Title: http.StatusText(http.StatusConflict), Status: http.StatusConflict,
		Detail: "entity is disabled", Code: "entity_disabled", CommandID: string(commandID),
	}
}
