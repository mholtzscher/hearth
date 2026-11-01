package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type EntityReader interface {
	GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error)
}
type Service struct {
	repo     Repository
	entities EntityReader
}

func NewService(repo Repository, entities EntityReader) *Service {
	return &Service{repo: repo, entities: entities}
}

func (s *Service) PresentView(ctx context.Context, d Definition) (Artifact, error) {
	if err := ValidateDefinition(d); err != nil {
		return Artifact{}, err
	}
	var graph viewGraph
	if err := json.Unmarshal(d.Spec, &graph); err != nil {
		return Artifact{}, err
	}
	for _, node := range graph.Elements {
		bindings, err := nodeBindings(node)
		if err != nil {
			return Artifact{}, err
		}
		for _, b := range bindings {
			if s.entities == nil {
				return Artifact{}, fmt.Errorf("entity reader unavailable")
			}
			entity, err := s.entities.GetEntity(ctx, devices.EntityID(b.EntityID))
			if errors.Is(err, devices.ErrEntityNotFound) {
				return Artifact{}, fmt.Errorf("%w: entity not found", ErrInvalidDefinition)
			}
			if err != nil {
				return Artifact{}, err
			}
			if string(entity.Entity.TypeID) != b.EntityType {
				return Artifact{}, fmt.Errorf("%w: entity type mismatch", ErrInvalidDefinition)
			}
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Artifact{}, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{Kind: "hearth.view", ArtifactID: "view_" + id.String(), Definition: raw}, nil
}

func validID(value, prefix string) bool {
	text, ok := strings.CutPrefix(value, prefix+"_")
	if !ok {
		return false
	}
	id, err := uuid.Parse(text)
	return err == nil && id.String() == text && id.Version() == 7 && id.Variant() == uuid.RFC4122
}
func ParseDashboardID(value string) (DashboardID, error) {
	if !validID(value, "dash") {
		return "", fmt.Errorf("invalid dashboard id")
	}
	return DashboardID(value), nil
}

// ParseArtifact accepts only a newly produced, strictly valid artifact.
func ParseArtifact(raw json.RawMessage) (Artifact, error) {
	a, err := ReadArtifact(raw)
	if err != nil {
		return Artifact{}, err
	}
	if _, err = ParseDefinition(a.Definition); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

// ReadArtifact retains bounded unsupported definitions for conversation history.
func ReadArtifact(raw json.RawMessage) (Artifact, error) {
	if len(raw) > MaxDefinitionBytes+1024 {
		return Artifact{}, fmt.Errorf("%w: artifact too large", ErrInvalidDefinition)
	}
	var a Artifact
	if err := json.Unmarshal(raw, &a); err != nil || a.Kind != "hearth.view" || !validID(a.ArtifactID, "view") {
		return Artifact{}, fmt.Errorf("%w: invalid artifact", ErrInvalidDefinition)
	}
	definition, err := ReadDefinition(a.Definition)
	if err != nil {
		return Artifact{}, err
	}
	a.Definition = definition
	return a, nil
}

func (s *Service) CreateDashboard(ctx context.Context, d Definition) (Dashboard, error) {
	if err := ValidateDefinition(d); err != nil {
		return Dashboard{}, err
	}
	return s.repo.CreateDashboard(ctx, d)
}
func (s *Service) GetDashboard(ctx context.Context, id DashboardID) (Dashboard, error) {
	if _, err := ParseDashboardID(string(id)); err != nil {
		return Dashboard{}, fmt.Errorf("%w: id", ErrInvalidDefinition)
	}
	return s.repo.GetDashboard(ctx, id)
}
func (s *Service) ListDashboards(ctx context.Context, p ListParams) (Page[Summary], error) {
	if p.Limit == 0 {
		p.Limit = 50
	}
	if p.Limit < 1 || p.Limit > 200 {
		return Page[Summary]{}, fmt.Errorf("%w: limit", ErrInvalidDefinition)
	}
	if p.AfterID != nil {
		if _, err := ParseDashboardID(string(*p.AfterID)); err != nil {
			return Page[Summary]{}, fmt.Errorf("%w: cursor", ErrInvalidDefinition)
		}
	}
	return s.repo.ListDashboards(ctx, p)
}
func (s *Service) SaveDashboard(ctx context.Context, p SaveDashboardParams) (Dashboard, error) {
	if _, err := ParseDashboardID(string(p.ID)); err != nil || p.ExpectedRevision < 1 {
		return Dashboard{}, fmt.Errorf("%w: id or revision", ErrInvalidDefinition)
	}
	if err := ValidateDefinition(p.Definition); err != nil {
		return Dashboard{}, err
	}
	return s.repo.SaveDashboard(ctx, p)
}
func (s *Service) DeleteDashboard(ctx context.Context, id DashboardID, revision int64) error {
	if _, err := ParseDashboardID(string(id)); err != nil || revision < 1 {
		return fmt.Errorf("%w: id or revision", ErrInvalidDefinition)
	}
	return s.repo.DeleteDashboard(ctx, id, revision)
}
