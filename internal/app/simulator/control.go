package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
)

const (
	// shutdownTimeout bounds control-channel drain on process shutdown.
	shutdownTimeout = 5 * time.Second
	// controlReadHeaderTimeout bounds the control channel's request header
	// reads, mirroring the core HTTP server's Slowloris protection.
	controlReadHeaderTimeout = 5 * time.Second
	// controlMaxBody bounds control publish bodies; values are small JSON.
	controlMaxBody = 64 << 10
)

// controlPublishResponse is the success body of a control publish: the Entity's
// flat snapshot fields plus exactly one canonical publication ID, observation_id
// for a State Entity or event_id for an event source. Failed publishes return an
// error body with no ID.
type controlPublishResponse struct {
	scripted.EntityInfo
	scripted.PublicationResult
}

// ServeControl runs the optional loopback control channel for one scripted
// runtime until ctx ends. It is a scripting aid for agents: the primary
// validation path stays hearthd's HTTP API and Device Facts.
func ServeControl(
	ctx context.Context,
	addr string,
	runtime *scripted.Runtime,
	logger *slog.Logger,
) error {
	if runtime == nil {
		return errors.New("control channel runtime is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sim/entities", func(writer http.ResponseWriter, _ *http.Request) {
		writeControlJSON(writer, http.StatusOK, runtime.Snapshot())
	})
	mux.HandleFunc(
		"POST /v1/sim/entities/{entity_id}/publish",
		func(writer http.ResponseWriter, request *http.Request) {
			entityID := request.PathValue("entity_id")
			body, readErr := io.ReadAll(http.MaxBytesReader(writer, request.Body, controlMaxBody))
			if readErr != nil {
				writeControlError(writer, http.StatusBadRequest, "unreadable publish body")
				return
			}
			result, publishErr := runtime.PublishEnvelope(
				request.Context(),
				entityID,
				json.RawMessage(body),
			)
			if publishErr != nil {
				writeControlError(writer, controlStatus(publishErr), publishErr.Error())
				return
			}
			info, snapshotErr := runtime.SnapshotFor(entityID)
			if snapshotErr != nil {
				writeControlError(writer, controlStatus(snapshotErr), snapshotErr.Error())
				return
			}
			writeControlJSON(writer, http.StatusOK, controlPublishResponse{
				EntityInfo:        info,
				PublicationResult: result,
			})
		},
	)
	mux.HandleFunc("POST /v1/sim/entities/{entity_id}/pause", func(writer http.ResponseWriter, request *http.Request) {
		entityID := request.PathValue("entity_id")
		if pauseErr := runtime.SetPaused(entityID, true); pauseErr != nil {
			writeControlError(writer, controlStatus(pauseErr), pauseErr.Error())
			return
		}
		writeControlEntity(writer, runtime, entityID)
	})
	mux.HandleFunc("POST /v1/sim/entities/{entity_id}/resume", func(writer http.ResponseWriter, request *http.Request) {
		entityID := request.PathValue("entity_id")
		if resumeErr := runtime.SetPaused(entityID, false); resumeErr != nil {
			writeControlError(writer, controlStatus(resumeErr), resumeErr.Error())
			return
		}
		writeControlEntity(writer, runtime, entityID)
	})
	listener, listenErr := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if listenErr != nil {
		return fmt.Errorf("listen on control channel: %w", listenErr)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: controlReadHeaderTimeout}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.InfoContext(ctx, "simulator control channel listening",
		slog.String("component", "simulator"),
		slog.String("event", "simulator.control_listening"),
		slog.String("address", listener.Addr().String()),
	)
	if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve control channel: %w", serveErr)
	}
	return nil
}

func controlStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	// Unknown Entities are 404; everything else is a bad value or a failed
	// publication.
	if _, ok := errors.AsType[*scripted.UnknownEntityError](err); ok {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func writeControlEntity(writer http.ResponseWriter, runtime *scripted.Runtime, entityID string) {
	info, err := runtime.SnapshotFor(entityID)
	if err != nil {
		writeControlError(writer, controlStatus(err), err.Error())
		return
	}
	writeControlJSON(writer, http.StatusOK, info)
}

func writeControlJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeControlError(writer http.ResponseWriter, status int, message string) {
	writeControlJSON(writer, status, map[string]string{"error": message})
}
