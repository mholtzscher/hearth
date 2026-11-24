//go:build integration

package agent //nolint:testpackage // Shared native dashboard tool fixtures.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func dashboardDefinitionFixture(t *testing.T, title string, large bool) json.RawMessage {
	t.Helper()
	elements := map[string]any{}
	children := []string{"a"}
	text := "Hello"
	if large {
		children = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
		text = strings.Repeat("x", 490)
	}
	for _, child := range children {
		elements[child] = map[string]any{"type": "Note", "props": map[string]any{"text": text}}
	}
	elements["root"] = map[string]any{"type": "Stack", "props": map[string]any{"gap": "small"}, "children": children}
	raw, err := json.Marshal(
		map[string]any{
			"schema_version":  1,
			"catalog_version": "hearth.dashboard/v1",
			"title":           title,
			"spec":            map[string]any{"root": "root", "elements": elements},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dashboards.ParseDefinition(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func composeArgumentsFixture(t *testing.T, title string, large bool) string {
	t.Helper()
	count, text := 1, "Hello"
	if large {
		count, text = 10, strings.Repeat("x", 490)
	}
	children := make([]map[string]string, count)
	for i := range children {
		children[i] = map[string]string{"type": "Note", "text": text}
	}
	raw, err := json.Marshal(map[string]any{
		"mode": "create", "title": title,
		"layout": map[string]any{"type": "Stack", "gap": "small", "children": children},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func configureViewAuthoring(t *testing.T, service *Service, authoring ViewAuthoring) {
	t.Helper()
	contract, err := newComposeViewContract(authoring)
	if err != nil {
		t.Fatal(err)
	}
	service.viewAuthoring = contract
}

type coordinatedAuthoring struct {
	ViewAuthoring

	compose func(context.Context, dashboards.ComposeViewInput) (dashboards.Artifact, error)
}

func (p coordinatedAuthoring) ComposeView(
	ctx context.Context,
	input dashboards.ComposeViewInput,
) (dashboards.Artifact, error) {
	return p.compose(ctx, input)
}

func publicHistoryJSON(t *testing.T, service *Service, conversationID string) json.RawMessage {
	t.Helper()
	handler := &Handler{service: service}
	output, err := handler.History(t.Context(), &HistoryInput{ID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(output.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
