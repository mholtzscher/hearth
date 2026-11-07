package automations

import (
	"encoding/json"
	"fmt"
)

const automationArtifactKind = "hearth.automation"

// Artifact is a presentation identity and one closed Automation variant.
type Artifact struct {
	ArtifactID   string
	Presentation Presentation
	raw          json.RawMessage
}

// MarshalJSON emits only fields belonging to the selected variant.
//
//nolint:goconst // Wire field names are clearer as literals.
func (artifact Artifact) MarshalJSON() ([]byte, error) {
	if artifact.raw != nil {
		return artifact.raw, nil
	}
	fields := map[string]any{"kind": automationArtifactKind, "artifact_id": artifact.ArtifactID, "schema_version": 1}
	switch value := artifact.Presentation.(type) {
	case PresentReference:
		fields["mode"], fields["automation_id"] = "reference", value.AutomationID
	case PresentCreate:
		fields["mode"], fields["definition_json"] = "create", value.DefinitionJSON
	case PresentReplace:
		fields["mode"], fields["automation_id"], fields["expected_revision"], fields["definition_json"] = "replace", value.AutomationID, value.ExpectedRevision, value.DefinitionJSON
	default:
		return nil, fmt.Errorf("invalid automation artifact variant")
	}
	return json.Marshal(fields)
}

// ReadArtifact keeps bounded unknown versions intact for history recovery.
func ReadArtifact(raw []byte) (Artifact, error) {
	if len(raw) > 1<<20 {
		return Artifact{}, fmt.Errorf("automation artifact too large")
	}
	var envelope struct {
		Kind          string `json:"kind"`
		ArtifactID    string `json:"artifact_id"`
		SchemaVersion int    `json:"schema_version"`
	}
	if err := json.Unmarshal(
		raw,
		&envelope,
	); err != nil || envelope.Kind != automationArtifactKind || validateAutomationUUID(envelope.ArtifactID, "aart") != nil ||
		envelope.SchemaVersion < 1 {
		return Artifact{}, fmt.Errorf("invalid automation artifact")
	}
	if envelope.SchemaVersion == 1 {
		return ParseArtifact(raw)
	}
	return Artifact{ArtifactID: envelope.ArtifactID, raw: append(json.RawMessage(nil), raw...)}, nil
}

// ParseArtifact validates a strict v1 artifact and its embedded definition.
//
//nolint:gocognit // The closed variant decoder keeps the complete shape checks together.
func ParseArtifact(raw []byte) (Artifact, error) {
	if len(raw) > 1<<20 {
		return Artifact{}, fmt.Errorf("automation artifact too large")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Artifact{}, err
	}
	var kind, id, mode string
	var version int
	if json.Unmarshal(fields["kind"], &kind) != nil || kind != automationArtifactKind ||
		json.Unmarshal(fields["artifact_id"], &id) != nil || validateAutomationUUID(id, "aart") != nil ||
		json.Unmarshal(fields["schema_version"], &version) != nil || version != 1 ||
		json.Unmarshal(fields["mode"], &mode) != nil {
		return Artifact{}, fmt.Errorf("invalid automation artifact envelope")
	}
	delete(fields, "kind")
	delete(fields, "artifact_id")
	delete(fields, "schema_version")
	delete(fields, "mode")
	var presentation Presentation
	switch mode {
	case "reference":
		var value struct {
			AutomationID AutomationID `json:"automation_id"`
		}
		if len(fields) != 1 || decodeArtifactFields(fields, &value) != nil {
			return Artifact{}, fmt.Errorf("invalid reference")
		}
		if _, err := ParseAutomationID(string(value.AutomationID)); err != nil {
			return Artifact{}, err
		}
		presentation = PresentReference{AutomationID: value.AutomationID}
	case "create":
		var value struct {
			DefinitionJSON string `json:"definition_json"`
		}
		if len(fields) != 1 || decodeArtifactFields(fields, &value) != nil {
			return Artifact{}, fmt.Errorf("invalid create")
		}
		if _, err := DecodeDefinition([]byte(value.DefinitionJSON)); err != nil {
			return Artifact{}, err
		}
		presentation = PresentCreate{DefinitionJSON: value.DefinitionJSON}
	case "replace":
		var value struct {
			AutomationID     AutomationID `json:"automation_id"`
			ExpectedRevision int64        `json:"expected_revision"`
			DefinitionJSON   string       `json:"definition_json"`
		}
		if len(fields) != 3 || decodeArtifactFields(fields, &value) != nil || value.ExpectedRevision < 1 {
			return Artifact{}, fmt.Errorf("invalid replacement")
		}
		if _, err := ParseAutomationID(string(value.AutomationID)); err != nil {
			return Artifact{}, err
		}
		if _, err := DecodeDefinition([]byte(value.DefinitionJSON)); err != nil {
			return Artifact{}, err
		}
		presentation = PresentReplace{
			AutomationID:     value.AutomationID,
			ExpectedRevision: value.ExpectedRevision,
			DefinitionJSON:   value.DefinitionJSON,
		}
	default:
		return Artifact{}, fmt.Errorf("unsupported automation artifact mode")
	}
	return Artifact{ArtifactID: id, Presentation: presentation}, nil
}

func decodeArtifactFields(fields map[string]json.RawMessage, target any) error {
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return DecodeStrictJSONObject(raw, target)
}
