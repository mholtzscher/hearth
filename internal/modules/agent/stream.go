package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

// RegisterStream mounts the turn-stream endpoint on the Echo router directly:
// Huma v2 has no SSE primitive, so it bypasses the OpenAPI group and does not
// appear in openapi.json.
func RegisterStream(router *echo.Echo, service Operations) {
	if router == nil || service == nil {
		return
	}
	handler := &streamHandler{service: service}
	router.POST("/v1/agent/conversations/:id/messages/stream", handler.sendStream)
}

type streamHandler struct {
	service Operations
}

type streamRequest struct {
	Text string `json:"text"`
}

const streamErrorField = "error"

// sendStream runs one turn and forwards its events as SSE frames. The
// conversation is checked before headers flush so unknown IDs still return a
// JSON 404; afterwards the outcome travels as turn.finished/turn.failed, and a
// client disconnect merely cancels the turn context. Persisted rows stay
// readable either way.
func (handler *streamHandler) sendStream(ctx *echo.Context) error {
	var body streamRequest
	if err := ctx.Bind(&body); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{streamErrorField: "invalid JSON body"})
	}
	if strings.TrimSpace(body.Text) == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{streamErrorField: "text is required"})
	}
	// Reject before headers flush so a drained agent still answers with JSON.
	if !handler.service.AdmissionOpen() {
		return ctx.JSON(
			http.StatusServiceUnavailable,
			map[string]string{streamErrorField: "agent admission is unavailable"},
		)
	}
	exists, err := handler.service.ConversationExists(ctx.Request().Context(), ctx.Param("id"))
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{streamErrorField: "internal error"})
	}
	if !exists {
		return ctx.JSON(http.StatusNotFound, map[string]string{streamErrorField: "agent conversation not found"})
	}
	writer := ctx.Response()
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	flush := func() {
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	flush()
	emit := func(event TurnEvent) {
		raw, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return
		}
		fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.Type, raw)
		flush()
	}
	_, streamErr := handler.service.SendMessageWithEvents(
		ctx.Request().Context(), ctx.Param("id"), body.Text, emit,
	)
	// turn.finished/turn.failed already traveled; a transport error here
	// only ends the stream.
	_ = streamErr
	return nil
}
