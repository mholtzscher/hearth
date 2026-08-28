package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func apiError(status int, message string) error {
	return huma.NewError(status, message)
}

type disabledCommandError struct {
	Type      string `json:"type"       format:"uri" default:"about:blank"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	CommandID string `json:"command_id"`
}

func (problem *disabledCommandError) Error() string  { return problem.Detail }
func (problem *disabledCommandError) GetStatus() int { return problem.Status }
func (problem *disabledCommandError) ContentType(contentType string) string {
	if contentType == "application/json" {
		return "application/problem+json"
	}
	return contentType
}

func entityDisabledProblem(commandID devices.CommandID) error {
	return &disabledCommandError{
		Type: "about:blank", Title: http.StatusText(http.StatusConflict), Status: http.StatusConflict,
		Detail: "entity is disabled", Code: "entity_disabled", CommandID: string(commandID),
	}
}
