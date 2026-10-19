package nats

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type FunctionRegistrar interface {
	RegisterCatalog(context.Context, adapters.Catalog) error
}

type functionDescriptor struct {
	Key          string          `json:"key"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	TimeoutMS    int             `json:"timeout_ms"`
}
type functionRegistrationRequest struct {
	CatalogRevision string               `json:"catalog_revision"`
	Functions       []functionDescriptor `json:"functions"`
}
type functionRegistrationResponse struct {
	Status          string `json:"status"`
	CatalogRevision string `json:"catalog_revision,omitempty"`
	ReasonCode      string `json:"reason_code,omitempty"`
}

type FunctionRegistrationServer struct {
	*platformnats.RequestReplyServer
}

func StartFunctionRegistrationServer(
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	registrar FunctionRegistrar,
	logger *slog.Logger,
) (*FunctionRegistrationServer, error) {
	if connection == nil || validator == nil || registrar == nil {
		return nil, adapters.ErrInvalidFunctionInput
	}
	if logger == nil {
		logger = slog.Default()
	}
	server, err := platformnats.StartRequestReplyServer(
		connection,
		validator,
		natswire.FunctionRegistrationWildcard(),
		"function registration",
		"catalog_revision",
		contractsv1.AdapterFunctionRegistrationRequestSchemaID,
		contractsv1.AdapterFunctionRegistrationResponseSchemaID,
		logger,
		func(ctx context.Context, subject string, request natswire.Envelope[functionRegistrationRequest]) (functionRegistrationResponse, bool) {
			route, routeErr := natswire.ParseFunctionRegistrationSubject(subject)
			if routeErr != nil || request.ID != request.Data.CatalogRevision {
				return functionRegistrationResponse{}, false
			}
			functions := make([]adapters.Descriptor, 0, len(request.Data.Functions))
			for _, descriptor := range request.Data.Functions {
				functions = append(
					functions,
					adapters.Descriptor{
						Key:          adapters.FunctionKey(descriptor.Key),
						Description:  descriptor.Description,
						InputSchema:  descriptor.InputSchema,
						OutputSchema: descriptor.OutputSchema,
						TimeoutMS:    descriptor.TimeoutMS,
					},
				)
			}
			bounded, cancel := context.WithTimeout(ctx, adapters.FunctionPersistenceTimeout)
			defer cancel()
			registerErr := registrar.RegisterCatalog(bounded, adapters.Catalog{ID: adapters.CatalogID(request.ID),
				AdapterID: route.AdapterID, RuntimeID: adapters.RuntimeID(route.RuntimeID), Functions: functions})
			var reason string
			switch {
			case errors.Is(registerErr, adapters.ErrRuntimeFenced):
				reason = "stale_runtime"
			case errors.Is(registerErr, adapters.ErrCatalogConflict):
				reason = "catalog_conflict"
			case errors.Is(registerErr, adapters.ErrInvalidFunctionInput):
				reason = "invalid_catalog"
			case registerErr != nil:
				logger.ErrorContext(
					ctx,
					"persist function catalog",
					slog.String("catalog_revision", request.ID),
					slog.String("error_code", "catalog_write_failed"),
				)
				return functionRegistrationResponse{}, false
			default:
				return functionRegistrationResponse{Status: "accepted", CatalogRevision: request.ID}, true
			}
			return functionRegistrationResponse{Status: "rejected", ReasonCode: reason}, true
		},
	)
	if err != nil {
		return nil, err
	}
	return &FunctionRegistrationServer{RequestReplyServer: server}, nil
}
