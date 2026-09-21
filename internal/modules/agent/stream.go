package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

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

// maxStreamBodyBytes bounds the decoded SSE request body. A message at
// maxMessageRunes stays well inside it even when every character arrives as a
// six-byte JSON escape.
const maxStreamBodyBytes = 64 << 10

// sendStream runs one turn and forwards its events as SSE frames. The
// conversation is checked before headers flush so unknown IDs still return a
// JSON 404; afterwards the outcome travels as turn.finished/turn.failed, and a
// client disconnect merely cancels the turn context. Persisted rows stay
// readable either way.
func (handler *streamHandler) sendStream(ctx *echo.Context) error {
	// The JSON serializer reads the whole body before decoding, so the bound has
	// to be installed on the request first.
	ctx.Request().Body = http.MaxBytesReader(ctx.Response(), ctx.Request().Body, maxStreamBodyBytes)
	var body streamRequest
	if err := ctx.Bind(&body); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			return ctx.JSON(
				http.StatusRequestEntityTooLarge,
				map[string]string{streamErrorField: "request body is too large"},
			)
		}
		return ctx.JSON(http.StatusBadRequest, map[string]string{streamErrorField: "invalid JSON body"})
	}
	// This route has no Huma schema, so shared validation is its only bound on
	// message text.
	if err := validateMessageText(body.Text); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{streamErrorField: err.Error()})
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
	// nginx buffers proxied responses by default, which would hold every event
	// until the turn ended and defeat the incremental stream.
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flush := func() {
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	flush()
	// Tool executions run on graph goroutines and emit outside the turn lock, so
	// one mutex covers the whole marshal/write/flush sequence: http.ResponseWriter
	// is not safe for concurrent use, and interleaved frames would corrupt the
	// stream.
	var writeMu sync.Mutex
	emit := func(event TurnEvent) {
		raw, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
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
