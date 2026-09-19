package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Conversation ID prefix, matching the house style of dev_/ent_/cmd_ IDs.
const conversationIDPrefix = "aconv_"

// rfc3339Nano matches the TEXT timestamp convention of the other tables.
const rfc3339Nano = time.RFC3339Nano

// defaultMaxSteps bounds one ReAct turn: each model+tools loop costs two
// steps, so 20 allows up to 9 tool rounds before the final answer.
const defaultMaxSteps = 20

// defaultSystemPrompt mirrors the dashboard prototype's instruction so both
// transports present the same household assistant. The tool catalog is the
// MCP catalog, so the prompt names what MCP offers: entities, devices,
// adapters and automations, the state/event/command/health histories, and
// command execution.
const defaultSystemPrompt = "You are a household assistant operating Hearth " +
	"through its tools. You can list and inspect entities, devices, adapters " +
	"and automations, read state, event, command and health histories, and " +
	"execute entity commands. " +
	"Prefer a read tool before acting. When executing a command, state the " +
	"entity_id and operation first, then summarize the outcome plainly with IDs."

// Turn event types streamed while a turn runs. The client renders tool
// activity incrementally and reconciles with history on turn.finished.
const (
	// EventTurnStarted opens one streamed turn.
	EventTurnStarted = "turn.started"
	// EventToolStarted announces one tool execution with its arguments.
	EventToolStarted = "tool.started"
	// EventToolFinished delivers one tool result (truncated for the stream;
	// the full row stays in history).
	EventToolFinished = "tool.finished"
	// EventTurnFinished closes the turn with the reply text.
	EventTurnFinished = "turn.finished"
	// EventTurnFailed closes the turn with a generic failure; the cause
	// stays server-side like every other boundary.
	EventTurnFailed = "turn.failed"
)

// maxEventResult bounds tool result bytes per stream event.
const maxEventResult = 4000

// TurnEvent is one streamed turn update.
type TurnEvent struct {
	Type      string   `json:"type"`
	Name      string   `json:"name,omitempty"`
	Arguments string   `json:"arguments,omitempty"`
	Result    string   `json:"result,omitempty"`
	Reply     string   `json:"reply,omitempty"`
	ToolCalls []string `json:"tool_calls,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// truncateEventResult keeps stream frames small; history keeps full rows.
func truncateEventResult(result string) string {
	if len(result) <= maxEventResult {
		return result
	}
	return result[:maxEventResult] + "…[truncated]"
}

// ErrConversationNotFound is returned when a turn or read names an unknown
// conversation.
var ErrConversationNotFound = errors.New("agent conversation not found")

// modelFailure marks turn-execution failures (agent build, model stream) so
// the transport can map them to a 502 without leaking the cause. Tool and
// persistence failures stay plain errors.
type modelFailure struct{ err error }

func (failure modelFailure) Error() string { return failure.err.Error() }
func (failure modelFailure) Unwrap() error { return failure.err }

func turnError(err error) error {
	return modelFailure{err: fmt.Errorf("agent turn: %w", err)}
}

// ModelConfig carries the chat-model credentials. Empty BaseURL selects the
// model provider's default endpoint. ReasoningEffort is sent verbatim when
// set: reasoning models such as gpt-5.6-luna reject function tools on chat
// completions unless it is "none".
type ModelConfig struct {
	APIKey          string
	Model           string
	BaseURL         string
	ReasoningEffort string
}

// Config assembles the agent service. The model is constructed once; one
// ReAct agent graph is compiled per turn from the shared model and tools.
// Tools come from MCPTools over the application's MCP server, so the agent
// and every other MCP client share one catalog. A nil Logger selects
// slog.Default.
type Config struct {
	DB       *sql.DB
	Tools    []tool.BaseTool
	Model    ModelConfig
	MaxSteps int
	Logger   *slog.Logger
}

// Service is the experimental household agent: Eino ReAct over the Devices
// service with SQLite conversation history.
type Service struct {
	database  *sql.DB
	chatModel model.ToolCallingChatModel
	tools     []tool.BaseTool
	maxSteps  int
	logger    *slog.Logger
}

// NewService builds the agent service and its chat model.
func NewService(ctx context.Context, config Config) (*Service, error) {
	switch {
	case config.DB == nil:
		return nil, errors.New("agent database is required")
	case len(config.Tools) == 0:
		return nil, errors.New("agent tools are required")
	case strings.TrimSpace(config.Model.APIKey) == "":
		return nil, errors.New("agent model API key is required")
	case strings.TrimSpace(config.Model.Model) == "":
		return nil, errors.New("agent model name is required")
	}
	modelConfig := &einoopenai.ChatModelConfig{
		APIKey: config.Model.APIKey,
		Model:  config.Model.Model,
	}
	if strings.TrimSpace(config.Model.BaseURL) != "" {
		modelConfig.BaseURL = config.Model.BaseURL
	}
	if strings.TrimSpace(config.Model.ReasoningEffort) != "" {
		modelConfig.ReasoningEffort = einoopenai.ReasoningEffortLevel(config.Model.ReasoningEffort)
	}
	chatModel, err := einoopenai.NewChatModel(ctx, modelConfig)
	if err != nil {
		return nil, fmt.Errorf("agent chat model: %w", err)
	}
	maxSteps := config.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		database:  config.DB,
		chatModel: chatModel,
		tools:     config.Tools,
		maxSteps:  maxSteps,
		logger:    logger,
	}, nil
}

// Conversation is one durable agent conversation.
type Conversation struct {
	ID        string
	CreatedAt time.Time
}

// StoredMessage is one persisted conversation row, decoded for readers.
type StoredMessage struct {
	ID        int64
	Role      string
	Content   string
	ToolCalls []string
	CreatedAt time.Time
}

// Turn is the result of one user message: the assistant's reply text plus the
// tool names the turn called, in order.
type Turn struct {
	Reply     string
	ToolCalls []string
}

// CreateConversation opens one durable conversation and returns its ID.
func (service *Service) CreateConversation(ctx context.Context) (Conversation, error) {
	raw, err := uuid.NewV7()
	if err != nil {
		return Conversation{}, fmt.Errorf("agent conversation id: %w", err)
	}
	conversation := Conversation{
		ID:        conversationIDPrefix + strings.ReplaceAll(raw.String(), "-", ""),
		CreatedAt: time.Now().UTC(),
	}
	_, err = service.database.ExecContext(ctx,
		`INSERT INTO agent_conversations(id, created_at) VALUES (?, ?)`,
		conversation.ID, conversation.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Conversation{}, fmt.Errorf("agent create conversation: %w", err)
	}
	return conversation, nil
}

// History reads one conversation back oldest-first.
func (service *Service) History(ctx context.Context, conversationID string) ([]StoredMessage, error) {
	if err := service.requireConversation(ctx, conversationID); err != nil {
		return nil, err
	}
	rows, err := service.database.QueryContext(ctx,
		`SELECT id, role, message_json, created_at FROM agent_messages
		  WHERE conversation_id = ? ORDER BY id`,
		conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("agent history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var history []StoredMessage
	for rows.Next() {
		var stored StoredMessage
		var raw string
		var createdAt string
		if scanErr := rows.Scan(&stored.ID, &stored.Role, &raw, &createdAt); scanErr != nil {
			return nil, fmt.Errorf("agent history: %w", scanErr)
		}
		var message schema.Message
		if unmarshalErr := json.Unmarshal([]byte(raw), &message); unmarshalErr != nil {
			return nil, fmt.Errorf("agent history: %w", unmarshalErr)
		}
		stored.Content = message.Content
		for _, call := range message.ToolCalls {
			stored.ToolCalls = append(stored.ToolCalls, call.Function.Name)
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, createdAt)
		if parseErr != nil {
			return nil, fmt.Errorf("agent history: %w", parseErr)
		}
		stored.CreatedAt = parsed
		history = append(history, stored)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("agent history: %w", rows.Err())
	}
	return history, nil
}

// ConversationSummary is one row in the conversation list: identity plus
// sidebar display fields (message count, last activity, and a preview taken
// from the first user message).
type ConversationSummary struct {
	ID            string
	CreatedAt     time.Time
	MessageCount  int
	LastMessageAt *time.Time
	Preview       string
}

// maxPreviewRunes bounds the sidebar preview taken from the first user
// message.
const maxPreviewRunes = 80

// ListConversations reads every conversation newest-activity-first with its
// message count and first-user-message preview for the dashboard sidebar.
func (service *Service) ListConversations(ctx context.Context) ([]ConversationSummary, error) {
	rows, err := service.database.QueryContext(ctx,
		`SELECT c.id, c.created_at, COUNT(m.id), MAX(m.created_at)
		   FROM agent_conversations AS c
		   LEFT JOIN agent_messages AS m ON m.conversation_id = c.id
		   GROUP BY c.id
		   ORDER BY COALESCE(MAX(m.created_at), c.created_at) DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("agent list conversations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var summaries []ConversationSummary
	for rows.Next() {
		var summary ConversationSummary
		var createdAt string
		var lastMessageAt sql.NullString
		if scanErr := rows.Scan(&summary.ID, &createdAt, &summary.MessageCount, &lastMessageAt); scanErr != nil {
			return nil, fmt.Errorf("agent list conversations: %w", scanErr)
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, createdAt)
		if parseErr != nil {
			return nil, fmt.Errorf("agent list conversations: %w", parseErr)
		}
		summary.CreatedAt = parsed
		if lastMessageAt.Valid {
			last, lastErr := time.Parse(time.RFC3339Nano, lastMessageAt.String)
			if lastErr != nil {
				return nil, fmt.Errorf("agent list conversations: %w", lastErr)
			}
			summary.LastMessageAt = &last
		}
		summaries = append(summaries, summary)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("agent list conversations: %w", rows.Err())
	}
	// Close before the preview lookups below: the database opens a single
	// connection, so a second query cannot run while these rows are open.
	if closeErr := rows.Close(); closeErr != nil {
		return nil, fmt.Errorf("agent list conversations: %w", closeErr)
	}
	for index := range summaries {
		if previewErr := service.attachPreview(ctx, &summaries[index]); previewErr != nil {
			return nil, previewErr
		}
	}
	return summaries, nil
}

// attachPreview fills the sidebar preview from the conversation's first user
// message; conversations without one keep an empty preview.
func (service *Service) attachPreview(ctx context.Context, summary *ConversationSummary) error {
	var raw string
	err := service.database.QueryRowContext(ctx,
		`SELECT message_json FROM agent_messages
		  WHERE conversation_id = ? AND role = ? ORDER BY id LIMIT 1`,
		summary.ID, "user",
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent list conversations: %w", err)
	}
	var message schema.Message
	if unmarshalErr := json.Unmarshal([]byte(raw), &message); unmarshalErr != nil {
		return fmt.Errorf("agent list conversations: %w", unmarshalErr)
	}
	preview := strings.Join(strings.Fields(message.Content), " ")
	if len([]rune(preview)) > maxPreviewRunes {
		preview = string([]rune(preview)[:maxPreviewRunes]) + "…"
	}
	summary.Preview = preview
	return nil
}

// SendMessage appends one user message, runs one ReAct turn over the rebuilt
// history, persists the tool trace plus the final message, and returns the
// reply.
func (service *Service) SendMessage(ctx context.Context, conversationID, text string) (Turn, error) {
	return service.SendMessageWithEvents(ctx, conversationID, text, nil)
}

// SendMessageWithEvents appends one user message, runs one ReAct turn, and
// reports turn.started/tool.started/tool.finished/turn.finished (or
// turn.failed) through emit as the turn runs. A nil emit disables events;
// history persistence is identical either way, so the blocking POST and the
// stream read the same stream of rows.
func (service *Service) SendMessageWithEvents(
	ctx context.Context,
	conversationID, text string,
	emit func(TurnEvent),
) (Turn, error) {
	fail := func(err error) (Turn, error) {
		service.logTurnFailure(ctx, conversationID, err)
		if emit != nil {
			emit(TurnEvent{Type: EventTurnFailed, Error: turnFailureText(err)})
		}
		return Turn{}, err
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Turn{}, errors.New("agent message text is required")
	}
	history, err := service.loadModelMessages(ctx, conversationID)
	if err != nil {
		return fail(err)
	}
	userMessage := schema.UserMessage(trimmed)
	if err := service.persistMessage(ctx, conversationID, userMessage); err != nil {
		return fail(err)
	}
	if emit != nil {
		emit(TurnEvent{Type: EventTurnStarted})
	}
	input := append(history, userMessage)
	assembler := newTurnAssembler(ctx, service, conversationID, emit)
	turnCtx := withAssembler(ctx, assembler)

	agentRunner, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: service.chatModel,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: service.tools},
		MaxStep:          service.maxSteps,
		MessageModifier: func(_ context.Context, messages []*schema.Message) []*schema.Message {
			withSystem := make([]*schema.Message, 0, len(messages)+1)
			withSystem = append(withSystem, &schema.Message{Role: schema.System, Content: defaultSystemPrompt})
			return append(withSystem, messages...)
		},
	})
	if err != nil {
		return fail(turnError(err))
	}
	// Generate, not Stream: the turn needs one final message, and tool
	// executions record their own history rows at call time through the turn
	// context above. The message stream carries model chunk deltas the spike
	// does not reassemble.
	final, err := agentRunner.Generate(turnCtx, input)
	if err != nil {
		return fail(turnError(err))
	}
	if final == nil {
		return fail(errors.New("agent turn: empty model response"))
	}
	turn, err := assembler.finishFinal(final)
	if err != nil {
		return fail(err)
	}
	if emit != nil {
		emit(TurnEvent{Type: EventTurnFinished, Reply: turn.Reply, ToolCalls: turn.ToolCalls})
	}
	return turn, nil
}

// turnFailureText maps a turn error to the generic client text, mirroring
// the Huma boundary: unknown conversations name themselves, everything else
// is one opaque failure.
func turnFailureText(err error) string {
	if errors.Is(err, ErrConversationNotFound) {
		return "agent conversation not found"
	}
	return "agent turn failed"
}

// ConversationExists reports whether a conversation row exists.
func (service *Service) ConversationExists(ctx context.Context, conversationID string) (bool, error) {
	err := service.requireConversation(ctx, conversationID)
	if errors.Is(err, ErrConversationNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// logTurnFailure records the model-side cause server-side with safe
// metadata only: the cause never crosses the HTTP boundary, mirroring the
// MCP error-mapping posture.
func (service *Service) logTurnFailure(ctx context.Context, conversationID string, err error) {
	service.logger.ErrorContext(ctx, "agent turn failed",
		slog.String("event", "agent.turn_failed"),
		slog.String("conversation_id", conversationID),
		slog.String("error", err.Error()),
	)
}

// assemblerKey carries the turn assembler to tool executions through the
// turn context: tool calls run on graph goroutines while the message stream
// is consumed elsewhere, and the shared context is the only channel between
// them. One turn owns one assembler, so concurrent turns never mix traces.
type assemblerKey struct{}

func withAssembler(ctx context.Context, assembler *turnAssembler) context.Context {
	return context.WithValue(ctx, assemblerKey{}, assembler)
}

func assemblerFrom(ctx context.Context) *turnAssembler {
	assembler, _ := ctx.Value(assemblerKey{}).(*turnAssembler)
	return assembler
}

// turnAssembler collects one turn's tool trace. Tool executions record
// their rows at call time through the turn context, so history persists in
// true execution order ahead of the final message. Every method takes mu;
// helpers ending in Locked assume it is held.
type turnAssembler struct {
	ctx            context.Context
	service        *Service
	conversationID string
	emit           func(TurnEvent)
	mu             sync.Mutex
	persisted      []*schema.Message
	tools          []string
	callSeq        int
}

func newTurnAssembler(
	ctx context.Context,
	service *Service,
	conversationID string,
	emit func(TurnEvent),
) *turnAssembler {
	return &turnAssembler{ctx: ctx, service: service, conversationID: conversationID, emit: emit}
}

// emitEvent reports one turn event when streaming; nil emit disables it.
// It runs outside mu: stream writes must never hold the turn lock.
func (assembler *turnAssembler) emitEvent(event TurnEvent) {
	if assembler.emit == nil {
		return
	}
	assembler.emit(event)
}

// finishFinal persists the turn's final message after the tool rows already
// recorded, and returns the turn result: the reply text plus tool names in
// call order.
func (assembler *turnAssembler) finishFinal(final *schema.Message) (Turn, error) {
	assembler.mu.Lock()
	defer assembler.mu.Unlock()
	if err := assembler.storeLocked(final); err != nil {
		return Turn{}, err
	}
	return Turn{Reply: final.Content, ToolCalls: assembler.tools}, nil
}

// recordToolCall persists one tool execution in execution order: the
// synthetic call row, then its result row. Synthetic call IDs are
// turn-scoped and consistent between the two rows. A persist failure fails
// the tool call rather than leaving a gap in the stream the next turn would
// rebuild from.
func (assembler *turnAssembler) recordToolCall(name, args, output string, callErr error) error {
	assembler.mu.Lock()
	assembler.callSeq++
	id := fmt.Sprintf("call-%d", assembler.callSeq)
	result := output
	if callErr != nil {
		result = "error: " + callErr.Error()
	}
	storeErr := assembler.storeLocked(&schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: id, Type: "function",
			Function: schema.FunctionCall{Name: name, Arguments: args}}},
	})
	if storeErr == nil {
		storeErr = assembler.storeLocked(&schema.Message{
			Role: schema.Tool, ToolCallID: id, ToolName: name, Content: result,
		})
	}
	if storeErr == nil {
		assembler.tools = append(assembler.tools, name)
	}
	finished := TurnEvent{
		Type: EventToolFinished, Name: name, Result: truncateEventResult(result),
	}
	// Emit outside the lock: a stalled stream client must never hold the
	// turn mutex while tool executions wait on it.
	assembler.mu.Unlock()
	if storeErr != nil {
		return storeErr
	}
	assembler.emitEvent(finished)
	return nil
}

// storeLocked persists one whole message and records execution order.
func (assembler *turnAssembler) storeLocked(message *schema.Message) error {
	if err := assembler.service.persistMessage(assembler.ctx, assembler.conversationID, message); err != nil {
		return err
	}
	assembler.persisted = append(assembler.persisted, message)
	return nil
}

// requireConversation maps a missing conversation row to the 404 sentinel.
func (service *Service) requireConversation(ctx context.Context, conversationID string) error {
	var present int
	if err := service.database.QueryRowContext(ctx,
		`SELECT 1 FROM agent_conversations WHERE id = ?`, conversationID,
	).Scan(&present); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConversationNotFound
		}
		return fmt.Errorf("agent conversation: %w", err)
	}
	return nil
}

// loadModelMessages rebuilds one conversation oldest-first for the next turn.
func (service *Service) loadModelMessages(ctx context.Context, conversationID string) ([]*schema.Message, error) {
	if err := service.requireConversation(ctx, conversationID); err != nil {
		return nil, err
	}
	rows, err := service.database.QueryContext(ctx,
		`SELECT message_json FROM agent_messages WHERE conversation_id = ? ORDER BY id`,
		conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("agent history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var history []*schema.Message
	for rows.Next() {
		var raw string
		if scanErr := rows.Scan(&raw); scanErr != nil {
			return nil, fmt.Errorf("agent history: %w", scanErr)
		}
		var message schema.Message
		if unmarshalErr := json.Unmarshal([]byte(raw), &message); unmarshalErr != nil {
			return nil, fmt.Errorf("agent history: %w", unmarshalErr)
		}
		history = append(history, &message)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("agent history: %w", rows.Err())
	}
	return history, nil
}

// persistMessage appends one message to the conversation stream.
func (service *Service) persistMessage(ctx context.Context, conversationID string, message *schema.Message) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("agent persist message: %w", err)
	}
	_, err = service.database.ExecContext(ctx,
		`INSERT INTO agent_messages(conversation_id, role, message_json, created_at)
		  VALUES (?, ?, ?, ?)`,
		conversationID, string(message.Role), string(raw), time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("agent persist message: %w", err)
	}
	return nil
}
