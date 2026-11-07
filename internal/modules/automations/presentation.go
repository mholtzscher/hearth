package automations

import (
	"context"
	"fmt"
)

// Presentation is a closed set of read-only presentation requests.
type Presentation interface{ presentation() }

// PresentReference follows the current head of a saved Automation.
type PresentReference struct{ AutomationID AutomationID }

func (PresentReference) presentation() {}

// PresentCreate proposes a new definition without saving it.
type PresentCreate struct{ DefinitionJSON string }

func (PresentCreate) presentation() {}

// PresentReplace proposes replacement against a selected revision.
type PresentReplace struct {
	AutomationID     AutomationID
	ExpectedRevision int64
	DefinitionJSON   string
}

func (PresentReplace) presentation() {}

// PresentAutomation validates the current references without writing or admitting a Run.
func (service *Service) PresentAutomation(ctx context.Context, input Presentation) (Artifact, error) {
	var target AutomationID
	var source string
	switch value := input.(type) {
	case PresentReference:
		target = value.AutomationID
	case PresentCreate:
		source = value.DefinitionJSON
	case PresentReplace:
		target, source = value.AutomationID, value.DefinitionJSON
		if value.ExpectedRevision < 1 {
			return Artifact{}, fmt.Errorf("%w: invalid revision", ErrInvalidAutomation)
		}
	default:
		return Artifact{}, fmt.Errorf("%w: invalid presentation", ErrInvalidAutomation)
	}
	if target != "" {
		if _, err := ParseAutomationID(string(target)); err != nil {
			return Artifact{}, err
		}
		if _, err := service.repository.GetAutomation(ctx, target); err != nil {
			return Artifact{}, err
		}
	} else if _, ok := input.(PresentReference); ok {
		return Artifact{}, fmt.Errorf("%w: automation ID required", ErrInvalidAutomation)
	} else if _, replacing := input.(PresentReplace); replacing {
		return Artifact{}, fmt.Errorf("%w: automation ID required", ErrInvalidAutomation)
	}
	if _, reference := input.(PresentReference); !reference {
		definition, err := DecodeDefinition([]byte(source))
		if err != nil {
			return Artifact{}, err
		}
		if _, err = service.prepareDefinition(ctx, definition); err != nil {
			return Artifact{}, err
		}
	}
	id, err := newAutomationUUID("automation artifact")
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{ArtifactID: "aart_" + id, Presentation: input}, nil
}
