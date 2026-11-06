package dashboards

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type EntityReader interface {
	GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error)
}
type DeviceReader interface {
	GetDevice(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error)
}
type Service struct {
	repo     Repository
	entities EntityReader
	devices  DeviceReader
}

func NewService(repo Repository, entities EntityReader, deviceReader DeviceReader) *Service {
	return &Service{repo: repo, entities: entities, devices: deviceReader}
}

func (s *Service) PresentView(ctx context.Context, d Definition) (Artifact, error) {
	if err := s.validateBoundDefinition(ctx, d); err != nil {
		return Artifact{}, err
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

func (s *Service) validateBoundDefinition(ctx context.Context, d Definition) error {
	if err := ValidateDefinition(d); err != nil {
		return err
	}
	bindings, err := collectBindings(d.Spec)
	if err != nil {
		return err
	}
	return validateBindingResources(ctx, s.entities, s.devices, bindings)
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
	if len(raw) > MaxDefinitionBytes+1024 {
		return Artifact{}, fmt.Errorf("%w: artifact too large", ErrInvalidDefinition)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope) != 3 {
		return Artifact{}, fmt.Errorf("%w: invalid artifact", ErrInvalidDefinition)
	}
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
	if err := s.validateBoundDefinition(ctx, d); err != nil {
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
	if err := s.validateBoundDefinition(ctx, p.Definition); err != nil {
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
