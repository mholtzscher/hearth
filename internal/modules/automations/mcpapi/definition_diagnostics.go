package mcpapi

import "github.com/mholtzscher/hearth/internal/modules/automations"

// Diagnostic metadata is flattened by ToolError beside failure_code/message.
type definitionDiagnostic struct {
	Code        string                `json:"code"`
	Path        string                `json:"path"`
	StepID      string                `json:"step_id,omitempty"`
	ConditionID string                `json:"condition_id,omitempty"`
	Span        *definitionSourceSpan `json:"span,omitempty"`
}

type definitionSourceSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func definitionDiagnostics(issues []automations.DefinitionIssue) []definitionDiagnostic {
	diagnostics := make([]definitionDiagnostic, 0, len(issues))
	for _, issue := range issues {
		code := issue.Code
		if code == "" {
			code = "invalid_definition"
		}
		diagnostic := definitionDiagnostic{
			Code:        code,
			Path:        "/definition" + issue.Path,
			StepID:      string(issue.StepID),
			ConditionID: string(issue.ConditionID),
		}
		if issue.Span != nil {
			diagnostic.Span = &definitionSourceSpan{Start: issue.Span.Start, End: issue.Span.End}
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics
}

func functionReferenceDiagnostics(reference *automations.FunctionReferenceError) []definitionDiagnostic {
	path := reference.Path
	if path == "" {
		path = "/steps"
	}
	return []definitionDiagnostic{
		{Code: reference.ReasonCode, Path: "/definition" + path, StepID: string(reference.StepID)},
	}
}
