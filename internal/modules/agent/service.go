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

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// conversationIDPrefix follows the house dev_/ent_/cmd_ ID style.
const conversationIDPrefix = "aconv_"

// rfc3339Nano matches the TEXT timestamp convention of the other tables.
const rfc3339Nano = time.RFC3339Nano

// defaultMaxSteps bounds one ReAct turn: each model+tools loop costs two
// steps, so 20 allows up to 9 tool rounds before the final answer.
const defaultMaxSteps = 20

// defaultSystemPrompt gives every conversation the same household-assistant
// behavior.
const defaultSystemPrompt = "You are a household assistant operating Hearth " +
	"through its tools. You can list and inspect entities, devices, adapters " +
	"and automations, read state, event, command and health histories, and " +
	"execute entity commands. " +
	"Prefer a read tool before acting. When executing a command, state the " +
	"entity_id and operation first, then summarize the outcome plainly with IDs. " +
	"Before creating or replacing an Automation, inspect each referenced Entity's state.value. " +
	"Observation comparison value_pointer fields address that value directly: use value_pointer \"\" for a scalar " +
	"number, boolean, or string, and use an RFC 6901 path only for a nested object or array; never " +
	"use /state/value. After saving, read the Automation back and inspect its history after the next " +
	"matching observation when practical."

// Turn event types streamed while a turn runs; clients render tool activity
// incrementally and reconcile with history on turn.finished.
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

// maxMessageRunes bounds one user message. The blocking transport repeats the
// same 4000 in its Huma maxLength tag, which cannot reference a constant.
const maxMessageRunes = 4000

// historyRowBudget bounds how many persisted rows one turn rebuilds from a
// conversation. One turn writes a user row, a tool call and tool result row per
// tool call, and a final assistant row, so this admits roughly the newest
// twenty tool-using turns.
const historyRowBudget = 200

// historyByteBudget bounds the newest persisted rows one turn rebuilds. Bytes,
// not tokens, keep the bound provider-independent, and JSON overhead makes the
// count conservative. Only whole recent turns inside it are kept, so an active
// conversation cannot grow until the provider rejects every later turn.
const historyByteBudget = 256 * 1024

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

// messageTextError marks user text rejected before any work: empty after
// trimming, or longer than maxMessageRunes. Its detail is fixed client-facing
// text, so transports map it to a 400 problem without echoing the rejected
// value and malformed input is never reported as a server fault.
type messageTextError struct{ detail string }

func (invalid messageTextError) Error() string { return invalid.detail }

// validateMessageText rejects the user text neither transport accepts as a
// message. The blocking route's Huma schema declares the same empty and length
// bounds; this check backs it up and is the only bound the schema-less SSE
// route has.
func validateMessageText(text string) error {
	trimmed := strings.TrimSpace(text)
	switch {
	case trimmed == "":
		return messageTextError{detail: "agent message text is required"}
	case len([]rune(trimmed)) > maxMessageRunes:
		return messageTextError{
			detail: fmt.Sprintf("agent message text must be at most %d characters", maxMessageRunes),
		}
	}
	return nil
}

// modelError marks turn-execution failures (agent build, model stream) so the
// transport can map them to a 502 without leaking the cause. Tool and
// persistence failures stay plain errors.
type modelError struct{ err error }

func (modelErr modelError) Error() string { return modelErr.err.Error() }
func (modelErr modelError) Unwrap() error { return modelErr.err }

func turnError(err error) error {
	return modelError{err: fmt.Errorf("agent turn: %w", err)}
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

// Config assembles the agent service. The model is shared, while each turn
// compiles a ReAct graph. ChatModel replaces the constructed provider model for
// tests and embedding; when it is set, Model credentials are not required. A
// nil Logger selects [slog.Default].
type Config struct {
	DB        *sql.DB
	Tools     []tool.BaseTool
	Model     ModelConfig
	ChatModel model.ToolCallingChatModel
	MaxSteps  int
	// Retention bounds whole-conversation history. It must be at least
	// MinimumConversationRetention for PruneHistory to run; zero is
	// unconfigured and fails safely at prune time, never at construction.
	Retention time.Duration
	Logger    *slog.Logger
}

// ErrAdmissionUnavailable reports that agent turn admission is closed, so
// StopAdmission or Drain has run and new turns are rejected.
var ErrAdmissionUnavailable = errors.New("agent admission is unavailable")

// Service is the household agent: Eino ReAct over the Core MCP catalog with
// durable SQLite conversation history.
type Service struct {
	database  *sql.DB
	queries   *dbsqlc.Queries
	chatModel model.ToolCallingChatModel
	tools     []tool.BaseTool
	maxSteps  int
	retention time.Duration
	logger    *slog.Logger

	// admission gates new turns and tracks admitted ones for Drain.
	admission *lifecycle.AdmissionGroup
	// turnsMu guards turns, whose cancel functions let Drain cancel a running
	// turn before joining it through admission.
	turnsMu  sync.Mutex
	turns    map[uint64]context.CancelFunc
	nextTurn uint64

	// gatesMu guards gates, which serialize turns per conversation so two
	// clients cannot build the same history snapshot or interleave rows.
	gatesMu sync.Mutex
	gates   map[string]*conversationGate
}

// conversationGate is one conversation's turn semaphore: token admits one turn,
// and refs counts the turns holding or waiting on it so an idle conversation
// drops its entry instead of accumulating state.
type conversationGate struct {
	token chan struct{}
	refs  int
}

// NewService builds the agent service and its chat model.
func NewService(ctx context.Context, config Config) (*Service, error) {
	switch {
	case config.DB == nil:
		return nil, errors.New("agent database is required")
	case len(config.Tools) == 0:
		return nil, errors.New("agent tools are required")
	case config.ChatModel == nil && strings.TrimSpace(config.Model.APIKey) == "":
		return nil, errors.New("agent model API key is required")
	case config.ChatModel == nil && strings.TrimSpace(config.Model.Model) == "":
		return nil, errors.New("agent model name is required")
	}
	chatModel := config.ChatModel
	if chatModel == nil {
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
		constructed, err := einoopenai.NewChatModel(ctx, modelConfig)
		if err != nil {
			return nil, fmt.Errorf("agent chat model: %w", err)
		}
		chatModel = constructed
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
		queries:   dbsqlc.New(config.DB),
		chatModel: chatModel,
		tools:     config.Tools,
		maxSteps:  maxSteps,
		retention: config.Retention,
		logger:    logger,
		admission: lifecycle.NewAdmissionGroup(),
		turns:     make(map[uint64]context.CancelFunc),
		gates:     make(map[string]*conversationGate),
	}, nil
}

// AdmissionOpen reports whether new turns are admitted.
func (service *Service) AdmissionOpen() bool {
	if service.admission == nil {
		return false
	}
	return service.admission.AdmissionOpen()
}

// StopAdmission rejects new turns with [ErrAdmissionUnavailable]. It is
// idempotent and neither waits for nor cancels admitted turns.
func (service *Service) StopAdmission() {
	if service.admission != nil {
		service.admission.CloseAdmission()
	}
}

// Drain closes admission, cancels every active turn, and joins them. A context
// error stops waiting, not the cancellations; admission stays closed.
func (service *Service) Drain(ctx context.Context) error {
	service.StopAdmission()
	service.cancelActiveTurns()
	if service.admission == nil {
		return nil
	}
	return service.admission.Wait(ctx)
}

// acquireTurn reserves one admission slot, or reports that admission is
// closed. A hand-built Service without an admission group counts as closed.
func (service *Service) acquireTurn() (*lifecycle.Reservation, bool) {
	if service.admission == nil {
		return nil, false
	}
	return service.admission.TryAcquire()
}

// trackTurn records one active turn's cancel function for Drain and returns its
// handle.
func (service *Service) trackTurn(cancel context.CancelFunc) uint64 {
	service.turnsMu.Lock()
	defer service.turnsMu.Unlock()
	if service.turns == nil {
		service.turns = make(map[uint64]context.CancelFunc)
	}
	service.nextTurn++
	service.turns[service.nextTurn] = cancel
	return service.nextTurn
}

// untrackTurn forgets a finished turn.
func (service *Service) untrackTurn(id uint64) {
	service.turnsMu.Lock()
	defer service.turnsMu.Unlock()
	delete(service.turns, id)
}

// cancelActiveTurns cancels every registered turn. Cancellation is idempotent,
// so a turn that finished while the snapshot was taken is unaffected.
func (service *Service) cancelActiveTurns() {
	service.turnsMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(service.turns))
	for _, cancel := range service.turns {
		cancels = append(cancels, cancel)
	}
	service.turnsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// acquireConversationTurn waits for the conversation's turn slot and returns
// its release function. Waiting honors ctx, so Drain cancels a queued turn
// instead of letting it start after the running turn ends.
func (service *Service) acquireConversationTurn(ctx context.Context, conversationID string) (func(), error) {
	service.gatesMu.Lock()
	if service.gates == nil {
		service.gates = make(map[string]*conversationGate)
	}
	gate := service.gates[conversationID]
	if gate == nil {
		gate = &conversationGate{token: make(chan struct{}, 1)}
		service.gates[conversationID] = gate
	}
	gate.refs++
	service.gatesMu.Unlock()

	select {
	case gate.token <- struct{}{}:
		return func() { service.releaseConversationTurn(conversationID, gate) }, nil
	case <-ctx.Done():
		service.releaseConversationRef(conversationID, gate)
		return nil, ctx.Err()
	}
}

// releaseConversationTurn frees the slot and forgets this turn's interest in
// the conversation.
func (service *Service) releaseConversationTurn(conversationID string, gate *conversationGate) {
	<-gate.token
	service.releaseConversationRef(conversationID, gate)
}

// releaseConversationRef drops one turn's interest in a conversation gate and
// removes the gate when no turn holds or waits on it.
func (service *Service) releaseConversationRef(conversationID string, gate *conversationGate) {
	service.gatesMu.Lock()
	defer service.gatesMu.Unlock()
	gate.refs--
	if gate.refs == 0 && service.gates[conversationID] == gate {
		delete(service.gates, conversationID)
	}
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
	err = service.queries.CreateConversation(ctx, dbsqlc.CreateConversationParams{
		ID:        conversation.ID,
		CreatedAt: encodeAgentTimestamp(conversation.CreatedAt),
	})
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
	rows, err := service.queries.ListMessages(ctx, dbsqlc.ListMessagesParams{ConversationID: conversationID})
	if err != nil {
		return nil, fmt.Errorf("agent history: %w", err)
	}
	var history []StoredMessage
	for _, row := range rows {
		var message schema.Message
		if unmarshalErr := json.Unmarshal([]byte(row.MessageJson), &message); unmarshalErr != nil {
			return nil, fmt.Errorf("agent history: %w", unmarshalErr)
		}
		parsed, parseErr := decodeAgentTimestamp(row.CreatedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("agent history: %w", parseErr)
		}
		stored := StoredMessage{
			ID:        row.ID,
			Role:      row.Role,
			Content:   message.Content,
			CreatedAt: parsed,
		}
		for _, call := range message.ToolCalls {
			stored.ToolCalls = append(stored.ToolCalls, call.Function.Name)
		}
		history = append(history, stored)
	}
	return history, nil
}

// ConversationSummary is one conversation list row with its sidebar display
// fields.
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

// ListConversations reads every conversation newest-activity-first for the
// dashboard sidebar.
func (service *Service) ListConversations(ctx context.Context) ([]ConversationSummary, error) {
	rows, err := service.queries.ListConversationSummaries(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent list conversations: %w", err)
	}
	var summaries []ConversationSummary
	for _, row := range rows {
		parsed, parseErr := decodeAgentTimestamp(row.CreatedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("agent list conversations: %w", parseErr)
		}
		summary := ConversationSummary{
			ID:           row.ID,
			CreatedAt:    parsed,
			MessageCount: int(row.MessageCount),
		}
		if row.LastMessageAt.Valid {
			last, lastErr := decodeAgentTimestamp(row.LastMessageAt.String)
			if lastErr != nil {
				return nil, fmt.Errorf("agent list conversations: %w", lastErr)
			}
			summary.LastMessageAt = &last
		}
		if row.FirstUserMessageJson.Valid {
			preview, previewErr := previewFromMessageJSON(row.FirstUserMessageJson.String)
			if previewErr != nil {
				return nil, previewErr
			}
			summary.Preview = preview
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// previewFromMessageJSON renders the sidebar preview from a persisted message
// row: its content with whitespace collapsed and bounded by maxPreviewRunes.
func previewFromMessageJSON(raw string) (string, error) {
	var message schema.Message
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		return "", fmt.Errorf("agent list conversations: %w", err)
	}
	preview := strings.Join(strings.Fields(message.Content), " ")
	if len([]rune(preview)) > maxPreviewRunes {
		preview = string([]rune(preview)[:maxPreviewRunes]) + "…"
	}
	return preview, nil
}

// SendMessage runs one turn over the conversation without streaming events.
func (service *Service) SendMessage(ctx context.Context, conversationID, text string) (Turn, error) {
	return service.SendMessageWithEvents(ctx, conversationID, text, nil)
}

// SendMessageWithEvents runs one turn, reporting turn.started/tool.started/
// tool.finished/turn.finished (or turn.failed) through emit. A nil emit
// disables events; persistence is identical either way, so the blocking POST
// and the stream rebuild the same rows.
func (service *Service) SendMessageWithEvents(
	ctx context.Context,
	conversationID, text string,
	emit func(TurnEvent),
) (Turn, error) {
	if err := validateMessageText(text); err != nil {
		return Turn{}, err
	}
	trimmed := strings.TrimSpace(text)
	reservation, admitted := service.acquireTurn()
	if !admitted {
		// Close the streamed turn even if admission closed after the transport
		// pre-check, so an SSE client always sees a terminal event.
		emitTurnFailed(emit, ErrAdmissionUnavailable)
		return Turn{}, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	// The turn context is what Drain cancels; persistence and the model call
	// both honor it, so a cancelled turn writes no half-finished trace.
	turnCtx, cancelTurn := context.WithCancel(ctx)
	turnID := service.trackTurn(cancelTurn)
	defer func() {
		service.untrackTurn(turnID)
		cancelTurn()
	}()
	// A turn admitted just before admission closed can register after Drain's
	// cancel sweep; cancel it here so Drain's join is also a cancellation.
	if !service.AdmissionOpen() {
		cancelTurn()
	}
	fail := func(stage string, err error) (Turn, error) {
		service.logTurnFailure(turnCtx, conversationID, stage, err)
		emitTurnFailed(emit, err)
		return Turn{}, err
	}
	// Serialize turns within one conversation: two clients submitting at once
	// would otherwise rebuild the same history snapshot and interleave their
	// user, tool, and assistant rows.
	releaseTurn, gateErr := service.acquireConversationTurn(turnCtx, conversationID)
	if gateErr != nil {
		return fail(turnStageQueue, gateErr)
	}
	defer releaseTurn()
	history, err := service.loadModelMessages(turnCtx, conversationID)
	if err != nil {
		return fail(turnStageHistory, err)
	}
	userMessage := schema.UserMessage(trimmed)
	if persistErr := service.persistMessage(turnCtx, conversationID, userMessage); persistErr != nil {
		return fail(turnStagePersist, persistErr)
	}
	if emit != nil {
		emit(TurnEvent{Type: EventTurnStarted})
	}
	input := make([]*schema.Message, 0, len(history)+1)
	input = append(input, history...)
	input = append(input, userMessage)
	assembler := newTurnAssembler(turnCtx, service, conversationID, emit)
	graphCtx := withAssembler(turnCtx, assembler)

	agentRunner, err := service.newAgentRunner(turnCtx)
	if err != nil {
		return fail(turnStageModel, turnError(err))
	}
	// Generate, not Stream: the turn needs one final message, and tool
	// executions record their own rows at call time through the turn context.
	// The message stream's chunk deltas are not reassembled.
	final, err := agentRunner.Generate(graphCtx, input)
	if err != nil {
		return fail(turnStageModel, turnError(err))
	}
	if final == nil {
		return fail(turnStageModel, errors.New("agent turn: empty model response"))
	}
	turn, err := assembler.finishFinal(final)
	if err != nil {
		return fail(turnStagePersist, err)
	}
	if emit != nil {
		emit(TurnEvent{Type: EventTurnFinished, Reply: turn.Reply, ToolCalls: turn.ToolCalls})
	}
	return turn, nil
}

// newAgentRunner builds the ReAct graph for one turn: the shared chat model and
// MCP tool catalog, with the household system prompt ahead of the messages.
func (service *Service) newAgentRunner(ctx context.Context) (*react.Agent, error) {
	return react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: service.chatModel,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: service.tools},
		MaxStep:          service.maxSteps,
		MessageModifier: func(_ context.Context, messages []*schema.Message) []*schema.Message {
			withSystem := make([]*schema.Message, 0, len(messages)+1)
			withSystem = append(withSystem, &schema.Message{Role: schema.System, Content: defaultSystemPrompt})
			return append(withSystem, messages...)
		},
	})
}

// emitTurnFailed closes a streamed turn with the opaque text for err, so an SSE
// client always sees a terminal event even when the turn never ran. A nil emit
// disables events.
func emitTurnFailed(emit func(TurnEvent), err error) {
	if emit == nil {
		return
	}
	emit(TurnEvent{Type: EventTurnFailed, Error: turnFailureText(err)})
}

// turnFailureText maps a turn error to client text, mirroring the Huma
// boundary: unknown conversations, closed admission, and cancellation name
// themselves, everything else is opaque.
func turnFailureText(err error) string {
	switch {
	case errors.Is(err, ErrConversationNotFound):
		return "agent conversation not found"
	case errors.Is(err, ErrAdmissionUnavailable):
		return "agent admission is unavailable"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "agent turn canceled"
	default:
		return "agent turn failed"
	}
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

// Turn failure stages recorded with agent.turn_failed: the fixed operation
// that failed, so a log reader never needs the raw error text.
const (
	turnStageQueue   = "queue"
	turnStageHistory = "history"
	turnStagePersist = "persist"
	turnStageModel   = "model"
)

// turnFailureCode classifies one turn failure for logs. Provider, tool, and
// persistence error text can carry prompts, arguments, or rejected household
// values, so only this fixed code and the stage cross into a log record.
func turnFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrConversationNotFound):
		return "conversation_not_found"
	case errors.Is(err, ErrAdmissionUnavailable):
		return "admission_unavailable"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.As(err, &modelError{}):
		return "model_call_failed"
	default:
		return "turn_failed"
	}
}

// logTurnFailure records the cause server-side only; it never crosses the
// HTTP boundary, mirroring the MCP error-mapping posture. docs/logging.md
// forbids raw error strings here, so the record carries fixed codes instead.
func (service *Service) logTurnFailure(ctx context.Context, conversationID, stage string, err error) {
	service.logger.ErrorContext(ctx, "agent turn failed",
		slog.String("event", "agent.turn_failed"),
		slog.String("conversation_id", conversationID),
		slog.String("stage", stage),
		slog.String("error_code", turnFailureCode(err)),
	)
}

// assemblerKey carries the turn assembler to tool executions through the
// turn context: tool calls run on graph goroutines with no other channel back
// to the caller. One turn owns one assembler, so concurrent turns never mix
// traces.
type assemblerKey struct{}

func withAssembler(ctx context.Context, assembler *turnAssembler) context.Context {
	return context.WithValue(ctx, assemblerKey{}, assembler)
}

func assemblerFrom(ctx context.Context) *turnAssembler {
	assembler, _ := ctx.Value(assemblerKey{}).(*turnAssembler)
	return assembler
}

// turnAssembler collects one turn's tool trace. Tool executions record their
// rows at call time through the turn context, so history keeps true execution
// order ahead of the final message. Every method takes mu; helpers ending in
// Locked assume it is held.
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

// finishFinal persists the final message after the tool rows and returns the
// turn result.
func (assembler *turnAssembler) finishFinal(final *schema.Message) (Turn, error) {
	assembler.mu.Lock()
	defer assembler.mu.Unlock()
	if err := assembler.storeLocked(final); err != nil {
		return Turn{}, err
	}
	return Turn{Reply: final.Content, ToolCalls: assembler.tools}, nil
}

// recordToolCall persists one tool execution as a synthetic call row followed
// by its result row; both share a turn-scoped ID. The pair is written in one
// statement, so a cancelled or failed persist never leaves a call row without
// its result.
func (assembler *turnAssembler) recordToolCall(name, args, output string, callErr error) error {
	assembler.mu.Lock()
	assembler.callSeq++
	id := fmt.Sprintf("call-%d", assembler.callSeq)
	resultText := output
	if callErr != nil {
		resultText = "error: " + callErr.Error()
	}
	storeErr := assembler.storeToolPairLocked(
		&schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{ID: id, Type: "function",
				Function: schema.FunctionCall{Name: name, Arguments: args}}},
		},
		&schema.Message{
			Role: schema.Tool, ToolCallID: id, ToolName: name, Content: resultText,
		},
	)
	if storeErr == nil {
		assembler.tools = append(assembler.tools, name)
	}
	finished := TurnEvent{
		Type: EventToolFinished, Name: name, Result: truncateEventResult(resultText),
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

// storeLocked persists one message and records it; callers must hold mu.
func (assembler *turnAssembler) storeLocked(message *schema.Message) error {
	if err := assembler.service.persistMessage(assembler.ctx, assembler.conversationID, message); err != nil {
		return err
	}
	assembler.persisted = append(assembler.persisted, message)
	return nil
}

// storeToolPairLocked persists a tool call row and its result row in one
// statement and records both; callers must hold mu.
func (assembler *turnAssembler) storeToolPairLocked(call, result *schema.Message) error {
	if err := assembler.service.persistMessagePair(assembler.ctx, assembler.conversationID, call, result); err != nil {
		return err
	}
	assembler.persisted = append(assembler.persisted, call, result)
	return nil
}

// requireConversation maps a missing conversation row to the 404 sentinel.
func (service *Service) requireConversation(ctx context.Context, conversationID string) error {
	if _, err := service.queries.GetConversation(ctx, dbsqlc.GetConversationParams{ID: conversationID}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConversationNotFound
		}
		return fmt.Errorf("agent conversation: %w", err)
	}
	return nil
}

// historyRow is one decoded persisted message plus the room it took in the
// database, so the history budget is measured without re-marshaling.
type historyRow struct {
	role    string
	bytes   int
	message *schema.Message
}

// loadModelMessages rebuilds the next turn's input from the newest complete
// turns inside the history budgets. Whole-conversation retention prunes only
// idle conversations, so an active conversation needs this bound of its own:
// without it every turn resends every row and full tool result until the provider
// rejects the request, after which that conversation can never finish a turn.
func (service *Service) loadModelMessages(ctx context.Context, conversationID string) ([]*schema.Message, error) {
	if err := service.requireConversation(ctx, conversationID); err != nil {
		return nil, err
	}
	rows, err := service.queries.ListRecentMessageJson(ctx, dbsqlc.ListRecentMessageJsonParams{
		ConversationID: conversationID,
		Limit:          historyRowBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("agent history: %w", err)
	}
	// Rows arrive newest-first so the budget can be applied without loading the
	// whole conversation.
	decoded := make([]historyRow, 0, len(rows))
	for _, row := range rows {
		var message schema.Message
		if unmarshalErr := json.Unmarshal([]byte(row.MessageJson), &message); unmarshalErr != nil {
			return nil, fmt.Errorf("agent history: %w", unmarshalErr)
		}
		decoded = append(decoded, historyRow{role: row.Role, bytes: len(row.MessageJson), message: &message})
	}
	return boundConversationHistory(decoded), nil
}

// boundConversationHistory selects the newest rows inside historyByteBudget and
// returns them oldest-first for the model. It drops any window without a user
// message: the turn appends its own user message, and history that opened with
// a tool result whose assistant tool call was cut would be rejected as an
// incomplete sequence.
func boundConversationHistory(rows []historyRow) []*schema.Message {
	kept := 0
	var bytes int
	for kept < len(rows) {
		next := bytes + rows[kept].bytes
		if next > historyByteBudget && kept > 0 {
			break
		}
		bytes = next
		kept++
	}
	start := -1
	for index := range kept {
		if rows[index].role == string(schema.User) {
			start = index
		}
	}
	if start < 0 {
		return nil
	}
	history := make([]*schema.Message, 0, start+1)
	for index := start; index >= 0; index-- {
		history = append(history, rows[index].message)
	}
	return history
}

// persistMessage appends one message to a conversation.
func (service *Service) persistMessage(ctx context.Context, conversationID string, message *schema.Message) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("agent persist message: %w", err)
	}
	err = service.queries.InsertMessage(ctx, dbsqlc.InsertMessageParams{
		ConversationID: conversationID,
		Role:           string(message.Role),
		MessageJson:    string(raw),
		CreatedAt:      encodeAgentTimestamp(time.Now()),
	})
	if err != nil {
		return fmt.Errorf("agent persist message: %w", err)
	}
	return nil
}

// persistMessagePair appends a tool call row and its result row in one
// statement, so a cancelled or failed write leaves neither row behind and
// history never rebuilds an unpaired tool call.
func (service *Service) persistMessagePair(
	ctx context.Context,
	conversationID string,
	call, result *schema.Message,
) error {
	callRaw, err := json.Marshal(call)
	if err != nil {
		return fmt.Errorf("agent persist message: %w", err)
	}
	resultRaw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("agent persist message: %w", err)
	}
	createdAt := encodeAgentTimestamp(time.Now())
	err = service.queries.InsertMessagePair(ctx, dbsqlc.InsertMessagePairParams{
		ConversationID:   conversationID,
		Role:             string(call.Role),
		MessageJson:      string(callRaw),
		CreatedAt:        createdAt,
		ConversationID_2: conversationID,
		Role_2:           string(result.Role),
		MessageJson_2:    string(resultRaw),
		CreatedAt_2:      createdAt,
	})
	if err != nil {
		return fmt.Errorf("agent persist message: %w", err)
	}
	return nil
}
