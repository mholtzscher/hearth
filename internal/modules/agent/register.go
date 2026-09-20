package agent

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Operations is the narrow agent surface the HTTP and SSE transports call.
// Registration accepts it instead of *Service so application tests can stub it.
type Operations interface {
	CreateConversation(ctx context.Context) (Conversation, error)
	ListConversations(ctx context.Context) ([]ConversationSummary, error)
	SendMessage(ctx context.Context, conversationID, text string) (Turn, error)
	SendMessageWithEvents(
		ctx context.Context,
		conversationID, text string,
		emit func(TurnEvent),
	) (Turn, error)
	History(ctx context.Context, conversationID string) ([]StoredMessage, error)
	ConversationExists(ctx context.Context, conversationID string) (bool, error)
	AdmissionOpen() bool
}

// Register mounts the agent operations on api. A nil service registers
// nothing, which keeps transport fixtures lightweight.
func Register(api huma.API, service Operations) {
	if service == nil {
		return
	}
	handler := &Handler{service: service}
	huma.Register(api, huma.Operation{
		OperationID: "create-agent-conversation", Method: http.MethodPost, Path: "/agent/conversations",
		Summary: "Open one durable agent conversation", Tags: []string{agentTag},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.CreateConversation)
	huma.Register(api, huma.Operation{
		OperationID: "list-agent-conversations", Method: http.MethodGet, Path: "/agent/conversations",
		Summary: "List agent conversations newest-activity-first", Tags: []string{agentTag},
		Errors: []int{http.StatusInternalServerError},
	}, handler.ListConversations)
	huma.Register(api, huma.Operation{
		OperationID: "send-agent-message", Method: http.MethodPost, Path: "/agent/conversations/{id}/messages",
		Summary: "Send one message and run one agent turn", Tags: []string{agentTag},
		Errors: []int{
			http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable,
			http.StatusBadGateway, http.StatusInternalServerError,
		},
	}, handler.SendMessage)
	huma.Register(api, huma.Operation{
		OperationID: "list-agent-messages", Method: http.MethodGet, Path: "/agent/conversations/{id}/messages",
		Summary: "Read one agent conversation oldest-first", Tags: []string{agentTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.History)
}

const agentTag = "Agent"

// Handler is the Huma transport over the agent service.
type Handler struct {
	service Operations
}

// CreateConversationInput opens a conversation; no body is needed.
type CreateConversationInput struct{}

// CreateConversationOutput carries the new conversation identity.
type CreateConversationOutput struct {
	Body ConversationBody
}

// ConversationBody is the durable conversation identity.
//
//nolint:golines // Huma schema tags stay beside their fields.
type ConversationBody struct {
	ID        string `json:"id" doc:"Canonical agent conversation ID"`
	CreatedAt string `json:"created_at" doc:"Creation time (RFC3339)"`
}

// CreateConversation opens one durable agent conversation.
func (handler *Handler) CreateConversation(
	ctx context.Context,
	_ *CreateConversationInput,
) (*CreateConversationOutput, error) {
	conversation, err := handler.service.CreateConversation(ctx)
	if err != nil {
		return nil, internalError()
	}
	return &CreateConversationOutput{Body: ConversationBody{
		ID: conversation.ID, CreatedAt: conversation.CreatedAt.Format(rfc3339Nano),
	}}, nil
}

// ListConversationsInput has no parameters; one response carries the sidebar.
type ListConversationsInput struct{}

// ListConversationsOutput carries the conversation list newest-first.
type ListConversationsOutput struct {
	Body ListConversationsBody
}

// ListConversationsBody is the sidebar payload.
type ListConversationsBody struct {
	Conversations []ConversationSummaryBody `json:"conversations" doc:"Conversations, newest activity first"`
}

// ConversationSummaryBody is one conversation with sidebar display fields.
//
//nolint:golines // Huma schema tags stay beside their fields.
type ConversationSummaryBody struct {
	ID            string  `json:"id" doc:"Canonical agent conversation ID"`
	CreatedAt     string  `json:"created_at" doc:"Creation time, RFC3339"`
	MessageCount  int     `json:"message_count" doc:"Persisted message rows"`
	LastMessageAt *string `json:"last_message_at,omitempty" doc:"Newest message time, RFC3339"`
	Preview       string  `json:"preview" doc:"First user message, truncated for the sidebar"`
}

// ListConversations reads every conversation newest-activity-first.
func (handler *Handler) ListConversations(
	ctx context.Context,
	_ *ListConversationsInput,
) (*ListConversationsOutput, error) {
	summaries, err := handler.service.ListConversations(ctx)
	if err != nil {
		return nil, internalError()
	}
	body := ListConversationsBody{Conversations: make([]ConversationSummaryBody, len(summaries))}
	for index, summary := range summaries {
		entry := ConversationSummaryBody{
			ID: summary.ID, CreatedAt: summary.CreatedAt.Format(rfc3339Nano),
			MessageCount: summary.MessageCount, Preview: summary.Preview,
		}
		if summary.LastMessageAt != nil {
			formatted := summary.LastMessageAt.Format(rfc3339Nano)
			entry.LastMessageAt = &formatted
		}
		body.Conversations[index] = entry
	}
	return &ListConversationsOutput{Body: body}, nil
}

// SendMessageInput carries one user message for a conversation.
type SendMessageInput struct {
	ID   string `path:"id" doc:"Canonical agent conversation ID"`
	Body SendMessageBody
}

// SendMessageBody is the user text for one turn.
type SendMessageBody struct {
	Text string `json:"text" minLength:"1" maxLength:"4000" doc:"User message text"`
}

// SendMessageOutput carries the assistant reply and the tool trace.
type SendMessageOutput struct {
	Body TurnBody
}

// TurnBody is one completed turn: reply text plus tool names in call order.
//
//nolint:golines // Huma schema tags stay beside their fields.
type TurnBody struct {
	Reply     string   `json:"reply" doc:"Assistant reply text"`
	ToolCalls []string `json:"tool_calls" doc:"Tool names called this turn, in order"`
}

// SendMessage appends one user message and blocks until the turn completes,
// mirroring execute-entity-command: the reply is terminal when returned.
func (handler *Handler) SendMessage(
	ctx context.Context,
	input *SendMessageInput,
) (*SendMessageOutput, error) {
	turn, err := handler.service.SendMessage(ctx, input.ID, input.Body.Text)
	switch {
	case errors.Is(err, ErrConversationNotFound):
		return nil, huma.NewError(http.StatusNotFound, "agent conversation not found")
	case errors.Is(err, ErrAdmissionUnavailable):
		return nil, huma.NewError(http.StatusServiceUnavailable, "agent admission is unavailable")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return nil, huma.NewError(http.StatusServiceUnavailable, "agent turn canceled")
	case errors.As(err, &modelError{}):
		return nil, huma.NewError(http.StatusBadGateway, "agent model call failed")
	case err != nil:
		return nil, internalError()
	}
	return &SendMessageOutput{Body: TurnBody(turn)}, nil
}

// HistoryInput reads one conversation.
type HistoryInput struct {
	ID string `path:"id" doc:"Canonical agent conversation ID"`
}

// HistoryOutput carries the conversation oldest-first.
type HistoryOutput struct {
	Body HistoryBody
}

// HistoryBody is the persisted message stream.
type HistoryBody struct {
	Messages []MessageBody `json:"messages" doc:"Persisted messages, oldest first"`
}

// MessageBody is one persisted message with its tool-call trace.
//
//nolint:golines // Huma schema tags stay beside their fields.
type MessageBody struct {
	ID        int64    `json:"message_id" doc:"Persistence order key"`
	Role      string   `json:"role" doc:"Eino message role"`
	Content   string   `json:"content" doc:"Message text"`
	ToolCalls []string `json:"tool_calls" doc:"Tool names on an assistant message"`
	CreatedAt string   `json:"created_at" doc:"Persistence time, RFC3339"`
}

// History reads one conversation back oldest-first.
func (handler *Handler) History(
	ctx context.Context,
	input *HistoryInput,
) (*HistoryOutput, error) {
	history, err := handler.service.History(ctx, input.ID)
	switch {
	case errors.Is(err, ErrConversationNotFound):
		return nil, huma.NewError(http.StatusNotFound, "agent conversation not found")
	case err != nil:
		return nil, internalError()
	}
	body := HistoryBody{Messages: make([]MessageBody, len(history))}
	for index, stored := range history {
		body.Messages[index] = MessageBody{
			ID: stored.ID, Role: stored.Role, Content: stored.Content,
			ToolCalls: stored.ToolCalls, CreatedAt: stored.CreatedAt.Format(rfc3339Nano),
		}
	}
	return &HistoryOutput{Body: body}, nil
}

// internalError is the generic 500 problem: the cause never crosses the
// boundary. Sibling of the devices api helper, without its unwrap machinery.
func internalError() error {
	return huma.NewError(http.StatusInternalServerError, "internal error")
}
