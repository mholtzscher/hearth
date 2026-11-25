package agent

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerRefreshStarterQuestions(api huma.API, handler *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "refresh-agent-starter-questions",
		Method:      http.MethodPost,
		Path:        "/agent/starter-questions/refresh",
		Summary:     "Generate starter questions now, or wait for the pass already running",
		Tags:        []string{agentTag},
		Errors:      []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, handler.RefreshStarterQuestions)
}

// RefreshStarterQuestionsInput has no body.
type RefreshStarterQuestionsInput struct{}

// RefreshStarterQuestions runs one generation pass, or joins the running one,
// and returns the resulting questions. A failed pass keeps the previous
// questions: 502 for a model or reply failure, 503 when generation is off,
// admission is closed, or the caller went away.
func (handler *Handler) RefreshStarterQuestions(
	ctx context.Context,
	_ *RefreshStarterQuestionsInput,
) (*ListStarterQuestionsOutput, error) {
	err := handler.service.RefreshStarterQuestions(ctx)
	if err == nil {
		return &ListStarterQuestionsOutput{Body: starterQuestionsBody(handler.service.StarterQuestions())}, nil
	}
	refreshErr, staged := errors.AsType[*StarterQuestionRefreshError](err)
	switch {
	case errors.Is(err, ErrStarterQuestionsOff):
		return nil, huma.NewError(http.StatusServiceUnavailable, "starter questions are off")
	case errors.Is(err, ErrAdmissionUnavailable):
		return nil, huma.NewError(http.StatusServiceUnavailable, "agent admission is unavailable")
	case ctx.Err() != nil:
		return nil, huma.NewError(http.StatusServiceUnavailable, "starter question refresh canceled")
	case staged && (refreshErr.Stage == StarterQuestionStageModel ||
		refreshErr.Stage == StarterQuestionStageReply || refreshErr.Stage == StarterQuestionStageTimeout):
		return nil, huma.NewError(http.StatusBadGateway, "starter question generation failed")
	default:
		logHTTPFailure(ctx, handler.logger, "refresh-agent-starter-questions", "service", err)
		return nil, internalError()
	}
}
