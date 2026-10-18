// Package adapters owns canonical Adapter runtime and health evidence.
package adapters

import (
	"log/slog"
	"time"
)

type Stores struct {
	Runtimes RuntimeRepository
	Reads    ReadRepository
}

type Dependencies struct {
	Logger *slog.Logger
	Now    func() time.Time
}

type Service struct {
	stores       Stores
	dependencies Dependencies
}

func NewService(stores Stores, dependencies Dependencies) *Service {
	if dependencies.Logger == nil {
		dependencies.Logger = slog.Default().With("component", "adapters")
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	return &Service{stores: stores, dependencies: dependencies}
}
