package nats

import (
	"context"
	"errors"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	defaultOwnedMappingsLimit         = 50
	maximumOwnedMappingsCursorBytes   = 2048
	ownedMappingsInvalidCursorCode    = "invalid_cursor"
	ownedMappingsInvalidCursorMessage = "Owned mappings cursor is invalid"
)

type OwnedMappingLister interface {
	ListOwnedMappings(
		context.Context,
		string,
		devices.RuntimeID,
		devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error)
}

type OwnedMappingsServer struct {
	*requestReplyServer
}

func StartOwnedMappingsServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	lister OwnedMappingLister,
	logger *slog.Logger,
) (*OwnedMappingsServer, error) {
	if lister == nil {
		return nil, errors.New("owned mappings lister is required")
	}
	logger = defaultLogger(logger)
	server, err := startRequestReplyServer(
		connection, validator,
		natswire.OwnedMappingsWildcard(), "owned mappings", "mapping_id",
		contractsv1.OwnedMappingsRequestSchemaID, contractsv1.OwnedMappingsResponseSchemaID,
		logger,
		func(
			ctx context.Context,
			subject string,
			request natswire.Envelope[ownedMappingsRequest],
		) (ownedMappingsResponse, bool) {
			return handleOwnedMappings(ctx, subject, request, lister, logger)
		},
	)
	if err != nil {
		return nil, err
	}
	return &OwnedMappingsServer{requestReplyServer: server}, nil
}

func handleOwnedMappings(
	ctx context.Context,
	subject string,
	request natswire.Envelope[ownedMappingsRequest],
	lister OwnedMappingLister,
	logger *slog.Logger,
) (ownedMappingsResponse, bool) {
	route, err := natswire.ParseOwnedMappingsSubject(subject)
	if err != nil {
		logger.ErrorContext(ctx, "discarding owned mappings request with invalid subject",
			"subject", subject, "mapping_id", request.ID, "error", err)
		return ownedMappingsResponse{}, false
	}
	if len(request.Data.Cursor) > maximumOwnedMappingsCursorBytes {
		return rejectedOwnedMappings(ownedMappingsInvalidCursorCode, ownedMappingsInvalidCursorMessage), true
	}
	limit := defaultOwnedMappingsLimit
	if request.Data.Limit != nil {
		limit = *request.Data.Limit
	}
	var after *devices.OwnedMappingPosition
	if request.Data.Cursor != "" {
		after, err = decodeOwnedMappingCursor(request.Data.Cursor, route.AdapterID)
		if err != nil {
			return rejectedOwnedMappings(ownedMappingsInvalidCursorCode, ownedMappingsInvalidCursorMessage), true
		}
	}
	page, listErr := lister.ListOwnedMappings(
		ctx,
		route.AdapterID,
		devices.RuntimeID(route.RuntimeID),
		devices.OwnedMappingPageParams{After: after, Limit: limit},
	)
	switch {
	case listErr == nil:
		response, responseErr := acceptedOwnedMappings(route.AdapterID, page)
		if responseErr != nil {
			logger.ErrorContext(ctx, "map owned mappings response",
				"subject", subject, "mapping_id", request.ID, "error", responseErr)
			return ownedMappingsResponse{}, false
		}
		return response, true
	case errors.Is(listErr, devices.ErrRuntimeFenced):
		return rejectedOwnedMappings(runtimeFencedCode, runtimeFencedMessage), true
	case errors.Is(listErr, devices.ErrInvalidPage):
		return rejectedOwnedMappings(ownedMappingsInvalidCursorCode, ownedMappingsInvalidCursorMessage), true
	default:
		logger.ErrorContext(ctx, "list owned mappings",
			"subject", subject, "mapping_id", request.ID, "error", listErr)
		return ownedMappingsResponse{}, false
	}
}

func acceptedOwnedMappings(
	adapterID string,
	page devices.Page[devices.OwnedMapping],
) (ownedMappingsResponse, error) {
	items := make([]ownedMapping, len(page.Items))
	for index, item := range page.Items {
		items[index] = ownedMapping{
			BindingKey: item.BindingKey,
			DeviceID:   string(item.DeviceID),
			EntityKey:  item.EntityKey,
			EntityID:   string(item.EntityID),
		}
	}
	response := ownedMappingsResponse{Status: statusAccepted, Items: &items}
	if !page.HasMore {
		return response, nil
	}
	last := page.Items[len(page.Items)-1]
	nextCursor, err := encodeOwnedMappingCursor(adapterID, devices.OwnedMappingPosition{
		BindingKey: last.BindingKey,
		EntityKey:  last.EntityKey,
	})
	if err != nil {
		return ownedMappingsResponse{}, err
	}
	response.NextCursor = nextCursor
	return response, nil
}

func rejectedOwnedMappings(code, message string) ownedMappingsResponse {
	return ownedMappingsResponse{
		Status: statusRejected,
		Error:  &ownedMappingsError{Code: code, Message: message},
	}
}
