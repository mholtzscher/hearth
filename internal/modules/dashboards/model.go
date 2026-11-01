// Package dashboards owns portable views and independently saved dashboards.
package dashboards

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalidDefinition = errors.New("invalid view definition")
	ErrNotFound          = errors.New("dashboard not found")
	ErrRevisionConflict  = errors.New("dashboard revision conflict")
)

type DashboardID string
type Definition struct {
	SchemaVersion  int             `json:"schema_version"`
	CatalogVersion string          `json:"catalog_version"`
	Title          string          `json:"title"`
	Spec           json.RawMessage `json:"spec"`
}
type Artifact struct {
	Kind       string          `json:"kind"`
	ArtifactID string          `json:"artifact_id"`
	Definition json.RawMessage `json:"definition"`
}
type Dashboard struct {
	ID         DashboardID     `json:"id"`
	Revision   int64           `json:"revision"`
	Definition json.RawMessage `json:"definition"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}
type SaveDashboardParams struct {
	ID               DashboardID
	ExpectedRevision int64
	Definition       Definition
}
type ListParams struct {
	AfterID *DashboardID
	Limit   int
}
type Page[T any] struct {
	Items   []T
	HasMore bool
}
type Summary struct {
	ID        DashboardID `json:"id"`
	Title     string      `json:"title"`
	Revision  int64       `json:"revision"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}
