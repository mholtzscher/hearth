package httpapi

import (
	"strconv"
	"strings"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// definitionDiagnostic describes request structure, never source or rejected values.
type definitionDiagnostic struct {
	Code        string                `json:"code"`
	Path        string                `json:"path"                   doc:"JSON Pointer relative to the request body"`
	StepID      string                `json:"step_id,omitempty"`
	ConditionID string                `json:"condition_id,omitempty"`
	Span        *definitionSourceSpan `json:"span,omitempty"`
}

type definitionSourceSpan struct {
	Start int `json:"start" minimum:"0" doc:"Inclusive UTF-8 byte offset in the decoded expression"`
	End   int `json:"end"   minimum:"0" doc:"Exclusive UTF-8 byte offset in the decoded expression"`
}

func definitionDiagnostics(issues []automations.DefinitionIssue, prefix string) []definitionDiagnostic {
	diagnostics := make([]definitionDiagnostic, 0, len(issues))
	for _, issue := range issues {
		code := issue.Code
		if code == "" {
			code = "invalid_definition"
		}
		diagnostic := definitionDiagnostic{
			Code:        code,
			Path:        prefix + issue.Path,
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

func functionReferencePath(reference *automations.FunctionReferenceError) string {
	if reference.Path != "" {
		return reference.Path
	}
	return "/steps"
}

func definitionLocation(pointer string) string {
	var location strings.Builder
	location.WriteString("body")
	for part := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		if _, err := strconv.Atoi(part); err == nil {
			location.WriteString("[" + part + "]")
		} else {
			location.WriteString("." + part)
		}
	}
	return location.String()
}
