// Package typed adapts generic SDK Commands to typed operation handlers.
package typed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

var operationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type Command[P any] struct {
	ID            string
	CorrelationID string
	EntityID      string
	Parameters    P
	Deadline      time.Time
}

type Handler[P any] func(context.Context, Command[P], adapter.Responder) error
type ParameterDecoder[P any] func(json.RawMessage) (P, error)

type Route struct {
	entityID      string
	operationName string
	invoke        func(context.Context, adapter.Command, adapter.Responder) error
}

func Operation[P any](entityID, operationName string, decode ParameterDecoder[P], handler Handler[P]) (Route, error) {
	switch {
	case entityID == "":
		return Route{}, errors.New("typed command entity ID is required")
	case !operationNamePattern.MatchString(operationName):
		return Route{}, fmt.Errorf("typed command operation name %q is not subject-safe", operationName)
	case decode == nil:
		return Route{}, fmt.Errorf("typed command operation %q has no parameter decoder", operationName)
	case handler == nil:
		return Route{}, fmt.Errorf("typed command operation %q has no handler", operationName)
	}
	return Route{
		entityID:      entityID,
		operationName: operationName,
		invoke: func(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
			parameters, err := decode(command.Parameters)
			if err != nil {
				return fmt.Errorf("decode %q command parameters: %w", operationName, err)
			}
			deadline, err := time.Parse(time.RFC3339Nano, command.Deadline)
			if err != nil {
				return fmt.Errorf("decode %q command deadline: %w", operationName, err)
			}
			return handler(ctx, Command[P]{
				ID:            command.ID,
				CorrelationID: command.CorrelationID,
				EntityID:      command.EntityID,
				Parameters:    parameters,
				Deadline:      deadline,
			}, responder)
		},
	}, nil
}

func NewCommandHandler(routes ...Route) (adapter.CommandHandler, error) {
	if len(routes) == 0 {
		return nil, errors.New("at least one typed command route is required")
	}
	type routeKey struct {
		entityID      string
		operationName string
	}
	byKey := make(map[routeKey]Route, len(routes))
	for _, route := range routes {
		if route.entityID == "" || route.operationName == "" || route.invoke == nil {
			return nil, errors.New("invalid typed command route")
		}
		key := routeKey{entityID: route.entityID, operationName: route.operationName}
		if _, duplicate := byKey[key]; duplicate {
			return nil, fmt.Errorf("duplicate typed command route for entity %q operation %q", route.entityID, route.operationName)
		}
		byKey[key] = route
	}
	return func(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
		route, exists := byKey[routeKey{entityID: command.EntityID, operationName: command.OperationName}]
		if !exists {
			return fmt.Errorf("no typed command route for entity %q operation %q", command.EntityID, command.OperationName)
		}
		return route.invoke(ctx, command, responder)
	}, nil
}
