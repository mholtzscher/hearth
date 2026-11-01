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

func newUserMessage(input MessageInput) *schema.Message {
	message := schema.UserMessage(strings.TrimSpace(input.Text))
	if input.ViewContext != nil {
		message.Extra = map[string]any{viewContextKey: input.ViewContext}
	}
	return message
}

// MessageInput is one user message and its optional selected draft.
type MessageInput struct {
	Text        string       `json:"text"                   minLength:"1" maxLength:"4000"`
	ViewContext *ViewContext `json:"view_context,omitempty"`
}

// ViewContext contains the user's draft and optional saved base.
type ViewContext struct {
	Definition        dashboards.Definition   `json:"definition"`
	SourceDashboardID *dashboards.DashboardID `json:"source_dashboard_id,omitempty"`
	SourceRevision    *int64                  `json:"source_revision,omitempty"`
}

// UnmarshalJSON validates the original definition, before unknown fields can be lost.
func (v *ViewContext) UnmarshalJSON(raw []byte) error {
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
	if input.ViewContext != nil {
		if err := input.ViewContext.validate(); err != nil {
			return messageTextError{detail: "invalid view context"}
		}
	}
	return nil
}

func retainedViewContext(message *schema.Message) json.RawMessage {
	if message.Role != schema.User {
		return nil
	}
	value, ok := message.Extra[viewContextKey]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var view struct {
		Definition json.RawMessage `json:"definition"`
	}
	if json.Unmarshal(raw, &view) != nil {
		return nil
	}
	if _, err = dashboards.ReadDefinition(view.Definition); err != nil {
		return nil
	}
	return raw
}

// prepareModelMessage copies only user messages carrying our private context.
func prepareModelMessage(message *schema.Message) *schema.Message {
	if message.Role != schema.User || message.Extra == nil {
		return message
	}
	value, ok := message.Extra[viewContextKey]
	if !ok {
		return message
	}
	copyMessage := *message
	copyMessage.Extra = make(map[string]any, len(message.Extra))
	for k, v := range message.Extra {
		if k != viewContextKey {
			copyMessage.Extra[k] = v
		}
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
