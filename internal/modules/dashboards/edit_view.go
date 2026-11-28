package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// ViewChange is one scoped edit; its JSON discriminator selects a closed
// operation rather than permitting a patch to an arbitrary graph path.
type ViewChange struct{ op viewEdit }

type viewEdit interface {
	target() editTarget
	apply(*Definition, *viewGraph, string) error
}

type editTarget struct {
	property string
	card     string
}

func (change *ViewChange) UnmarshalJSON(raw []byte) error {
	var header struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return err
	}
	var op viewEdit
	switch header.Op {
	case "set_title":
		op = &setViewTitle{}
	case "set_forecast_periods":
		op = &setForecastPeriods{}
	case "set_chart_window":
		op = &setChartWindow{}
	default:
		return invalidDefinition("unknown edit operation")
	}
	if err := json.Unmarshal(raw, op); err != nil {
		return err
	}
	change.op = op
	return nil
}

func (s *Service) EditView(ctx context.Context, selected Definition, changes []ViewChange) (Artifact, error) {
	if len(changes) < 1 || len(changes) > 8 {
		return Artifact{}, invalidViewArgument("/changes", "an edit has 1 to 8 changes")
	}
	if err := ValidateDefinition(selected); err != nil {
		if errors.Is(err, ErrInvalidDefinition) {
			return Artifact{}, invalidViewArgument(
				"/changes",
				"the selected draft is not valid; open it in the dashboard editor",
			)
		}
		return Artifact{}, err
	}
	// Decoding makes an independent graph. Keep props as raw values except
	// while making a requested edit, so unrelated content is never rebuilt.
	var graph viewGraph
	if err := json.Unmarshal(selected.Spec, &graph); err != nil {
		return Artifact{}, err
	}
	applied := make(map[editTarget]bool, len(changes))
	for i, change := range changes {
		path := fmt.Sprintf("/changes/%d", i)
		if change.op == nil {
			return Artifact{}, invalidViewArgument(path, "an edit operation is required")
		}
		target := change.op.target()
		if applied[target] {
			return Artifact{}, invalidViewArgument(path, "an edit may change each property only once")
		}
		if err := change.op.apply(&selected, &graph, path); err != nil {
			return Artifact{}, err
		}
		applied[target] = true
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return Artifact{}, err
	}
	selected.Spec = raw
	artifact, err := s.PresentView(ctx, selected)
	if rejected, ok := errors.AsType[DefinitionError](err); ok {
		return Artifact{}, invalidDefinition(rejected.Reason + "; open the selected draft in the dashboard editor")
	}
	return artifact, err
}

type setViewTitle struct {
	Title string `json:"title"`
}

func (*setViewTitle) target() editTarget { return editTarget{property: viewTitleProperty} }

func (edit *setViewTitle) apply(definition *Definition, _ *viewGraph, _ string) error {
	definition.Title = edit.Title
	return nil
}

type setForecastPeriods struct {
	CardID  string `json:"card_id"`
	Periods int    `json:"periods"`
}

func (edit *setForecastPeriods) target() editTarget {
	return editTarget{property: "periods", card: edit.CardID}
}

func (edit *setForecastPeriods) apply(_ *Definition, graph *viewGraph, path string) error {
	return graph.editCard(edit.CardID, []string{forecastNode}, path, func(_ string, props rawViewProps) error {
		var source struct {
			EntityType devices.EntityTypeID `json:"entity_type"`
		}
		if err := json.Unmarshal(props["source"], &source); err != nil {
			return err
		}
		if err := validateForecastPeriods(source.EntityType, edit.Periods, path+"/periods"); err != nil {
			return err
		}
		return props.set("periods", edit.Periods)
	})
}

type setChartWindow struct {
	CardID string `json:"card_id"`
	Window string `json:"window"`
}

func (edit *setChartWindow) target() editTarget {
	return editTarget{property: "window", card: edit.CardID}
}

func (edit *setChartWindow) apply(_ *Definition, graph *viewGraph, path string) error {
	kinds := []string{timeSeriesNode, timeInRangeNode, rankingNode}
	return graph.editCard(edit.CardID, kinds, path, func(kind string, props rawViewProps) error {
		switch kind {
		case timeInRangeNode:
			return props.set("window", edit.Window)
		case rankingNode:
			return props.set("range", edit.Window)
		}
		if _, compared := props["compare"]; compared {
			return invalidViewArgument(path+"/card_id", "a TimeSeries with compare keeps its 24h window")
		}
		var series []rawViewProps
		if err := json.Unmarshal(props["series"], &series); err != nil {
			return err
		}
		for _, item := range series {
			var source rawViewProps
			if err := json.Unmarshal(item["source"], &source); err != nil {
				return err
			}
			if err := source.set("window", edit.Window); err != nil {
				return err
			}
			if err := item.set("source", source); err != nil {
				return err
			}
		}
		return props.set("series", series)
	})
}

type rawViewProps map[string]json.RawMessage

func (p rawViewProps) set(key string, value any) error {
	raw, err := json.Marshal(value)
	if err == nil {
		p[key] = raw
	}
	return err
}

func (g *viewGraph) editCard(id string, kinds []string, path string, apply func(string, rawViewProps) error) error {
	card, found := g.Elements[id]
	if !found || !slices.Contains(kinds, card.Type) {
		return invalidViewArgument(
			path+"/card_id", "choose a "+strings.Join(kinds, " or ")+" card key from the selected draft",
		)
	}
	var props rawViewProps
	if err := json.Unmarshal(card.Props, &props); err != nil {
		return err
	}
	if err := apply(card.Type, props); err != nil {
		return err
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return err
	}
	card.Props = raw
	g.Elements[id] = card
	return nil
}
