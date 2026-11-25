package agent

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerListStarterQuestions(api huma.API, handler *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-agent-starter-questions", Method: http.MethodGet, Path: "/agent/starter-questions",
		Summary: "Read the generated starter questions for an empty conversation", Tags: []string{agentTag},
		Errors: []int{http.StatusInternalServerError},
	}, handler.ListStarterQuestions)
}

// ListStarterQuestionsInput has no parameters.
type ListStarterQuestionsInput struct{}

// ListStarterQuestionsOutput carries the cached starter questions.
type ListStarterQuestionsOutput struct {
	Body StarterQuestionsBody
}

// StarterQuestionsBody is the current starter question cache.
type StarterQuestionsBody struct {
	StarterQuestions []StarterQuestionBody `json:"starter_questions" nullable:"false" doc:"Generated questions; empty before the first successful pass or when generation is off"`
	GeneratedAt      *string               `json:"generated_at"                       doc:"When the pass that produced these questions read the household (RFC3339); null before the first successful pass"`
}

// StarterQuestionBody is one suggested prompt.
type StarterQuestionBody struct {
	Title  string `json:"title"  doc:"Button text"`
	Prompt string `json:"prompt" doc:"Message sent when the button is tapped"`
}

// ListStarterQuestions reads the in-memory cache.
func (handler *Handler) ListStarterQuestions(
	_ context.Context,
	_ *ListStarterQuestionsInput,
) (*ListStarterQuestionsOutput, error) {
	cached := handler.service.StarterQuestions()
	body := StarterQuestionsBody{StarterQuestions: make([]StarterQuestionBody, len(cached.Questions))}
	for index, question := range cached.Questions {
		body.StarterQuestions[index] = StarterQuestionBody(question)
	}
	if !cached.GeneratedAt.IsZero() {
		generatedAt := cached.GeneratedAt.Format(rfc3339Nano)
		body.GeneratedAt = &generatedAt
	}
	return &ListStarterQuestionsOutput{Body: body}, nil
}
