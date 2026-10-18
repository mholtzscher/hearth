package adapters

import (
	"context"
	"errors"
)

var (
	ErrAdapterNotFound         = errors.New("adapter not found")
	ErrAdapterActive           = errors.New("adapter already has an active runtime")
	ErrRuntimeClaimConflict    = errors.New("adapter runtime claim conflict")
	ErrInvalidHealthTransition = errors.New("invalid adapter health transition")
	ErrAdapterUnhealthy        = errors.New("adapter unhealthy")
	ErrRuntimeFenced           = errors.New("adapter runtime fenced")
	ErrInvalidPage             = errors.New("invalid page")
	ErrCorruptHealthEvidence   = errors.New("corrupt adapter health evidence")
	ErrHealthRevisionOverflow  = errors.New("adapter health revision overflow")
)

type Page[T any] struct {
	Items   []T
	HasMore bool
}

func ValidPageLimit(limit int) bool { return limit >= 1 && limit <= 200 }

type RuntimeRepository interface {
	ClaimAdapterRuntime(context.Context, ClaimRuntimeWrite) error
	RecordAdapterHeartbeat(context.Context, HeartbeatWrite) (HeartbeatResult, error)
	ReleaseAdapterRuntime(context.Context, ReleaseRuntimeWrite) error
	ExpireAdapterLeases(context.Context, ExpireLeasesWrite) error
}

type ReadRepository interface {
	GetAdapter(context.Context, string) (AdapterInstance, error)
	ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error)
	ListAdapterHealthHistory(context.Context, ListAdapterHealthParams) (Page[HealthTransition], error)
}
