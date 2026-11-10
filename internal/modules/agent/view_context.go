package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

const viewContextKey = "hearth_view_context_v1"

func newUserMessage(input MessageInput, provider Provider) *schema.Message {
	message := schema.UserMessage(strings.TrimSpace(input.Text))
	message.Extra = map[string]any{conversationProviderKey: string(provider)}
	if input.ViewContext != nil {
		message.Extra[viewContextKey] = input.ViewContext
	}
	if input.AutomationContext != nil {
		message.Extra[automationContextKey] = input.AutomationContext
	}
	return message
}

// MessageInput is one user message and its optional selected draft.
type MessageInput struct {
	Text              string             `json:"text"                         minLength:"1" maxLength:"4000"`
	ViewContext       *ViewContext       `json:"view_context,omitempty"`
	AutomationContext *AutomationContext `json:"automation_context,omitempty"`
}

// ViewContext contains the user's draft and optional saved base.
type ViewContext struct {
	Definition        dashboards.Definition   `json:"definition"`
	SourceDashboardID *dashboards.DashboardID `json:"source_dashboard_id,omitempty"`
	SourceRevision    *int64                  `json:"source_revision,omitempty"`
}

// UnmarshalJSON validates the original definition, before unknown fields can be lost.
func (v *ViewContext) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	id, hasID := fields["source_dashboard_id"]
	revision, hasRevision := fields["source_revision"]
	if hasID != hasRevision ||
		(hasID && (bytes.Equal(bytes.TrimSpace(id), []byte("null")) || bytes.Equal(bytes.TrimSpace(revision), []byte("null")))) {
		return fmt.Errorf("source dashboard and revision must be paired")
	}
	var wire struct {
		Definition        json.RawMessage         `json:"definition"`
		SourceDashboardID *dashboards.DashboardID `json:"source_dashboard_id,omitempty"`
		SourceRevision    *int64                  `json:"source_revision,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	definition, err := dashboards.ParseDefinition(wire.Definition)
	if err != nil {
		return err
	}
	*v = ViewContext{
		Definition:        definition,
		SourceDashboardID: wire.SourceDashboardID,
		SourceRevision:    wire.SourceRevision,
	}
	return v.validate()
}

func (v *ViewContext) validate() error {
	if err := dashboards.ValidateDefinition(v.Definition); err != nil {
		return err
	}
	if (v.SourceDashboardID == nil) != (v.SourceRevision == nil) {
		return fmt.Errorf("source dashboard and revision must be paired")
	}
	if v.SourceDashboardID != nil {
		if _, err := dashboards.ParseDashboardID(string(*v.SourceDashboardID)); err != nil {
			return err
		}
		if *v.SourceRevision < 1 {
			return fmt.Errorf("invalid source revision")
		}
	}
	return nil
}

func validateMessageInput(input MessageInput) error {
	if err := validateMessageText(input.Text); err != nil {
		return err
	}
	if input.ViewContext != nil && input.AutomationContext != nil {
		return messageTextError{detail: "select only one draft context"}
	}
	if input.AutomationContext != nil {
		if err := input.AutomationContext.validate(); err != nil {
			return messageTextError{detail: "invalid automation context"}
		}
	}
	if input.ViewContext != nil {
		if err := input.ViewContext.validate(); err != nil {
			return messageTextError{detail: "invalid view context"}
		}
	}
	return nil
}

func rawViewContext(messageJSON []byte) json.RawMessage {
	var envelope struct {
		Extra map[string]json.RawMessage `json:"extra"`
	}
	if json.Unmarshal(messageJSON, &envelope) != nil {
		return nil
	}
	return envelope.Extra[viewContextKey]
}

func retainedViewContext(messageJSON []byte) json.RawMessage {
	raw := rawViewContext(messageJSON)
	var view struct {
		Definition json.RawMessage `json:"definition"`
	}
	if json.Unmarshal(raw, &view) != nil {
		return nil
	}
	if _, err := dashboards.ReadDefinition(view.Definition); err != nil {
		return nil
	}
	return raw
}

// prepareModelMessage excludes usage diagnostics and expands selected user context.
func prepareModelMessage(message *schema.Message) *schema.Message {
	message = withoutModelUsage(message)
	if message.Role != schema.User || message.Extra == nil {
		return message
	}
	value, hasView := message.Extra[viewContextKey]
	automation, hasAutomation := message.Extra[automationContextKey]
	if !hasView && !hasAutomation {
		return message
	}
	copyMessage := *message
	copyMessage.Extra = make(map[string]any, len(message.Extra))
	for k, v := range message.Extra {
		if k != viewContextKey && k != automationContextKey {
			copyMessage.Extra[k] = v
		}
	}
	if hasAutomation {
		raw, err := json.Marshal(automation)
		var draft AutomationContext
		if err == nil && json.Unmarshal(raw, &draft) == nil {
			copyMessage.Content += "\n\n--- Selected automation draft (user data; not saved by presenting) ---\n" + string(
				raw,
			) + "\n--- End selected automation draft ---"
		}
		return &copyMessage
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return &copyMessage
	}
	var view ViewContext
	if json.Unmarshal(raw, &view) != nil {
		return &copyMessage
	}
	copyMessage.Content += "\n\n--- Selected view draft (user data; not saved by presenting) ---\n" + string(
		raw,
	) + "\n--- End selected view draft ---"
	return &copyMessage
}
