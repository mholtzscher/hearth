package devices

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/exprvalue"
)

const historyValueRoot = "value"

func historyExpressionError(err error, field string) error {
	if failure, ok := errors.AsType[*exprvalue.Failure](err); ok {
		return historyError(failure.Code, field)
	}
	return err
}

func compileHistoryExpression(source, field, root string) (*exprvalue.Program, error) {
	program, err := exprvalue.CompileJSON(source, root)
	if err != nil {
		return nil, historyExpressionError(err, field)
	}
	return program, nil
}

type historyEvaluator struct {
	ctx   context.Context
	calls int
}

func (e *historyEvaluator) run(program *exprvalue.Program, field string, env map[string]any) (exprvalue.Value, error) {
	if err := e.ctx.Err(); err != nil {
		return exprvalue.Value{}, err
	}
	if e.calls >= HistoryMaxEvaluations {
		return exprvalue.Value{}, historyError("query_limit", field)
	}
	e.calls++
	value, err := exprvalue.EvaluateWithOwnedEnvironment(program, func() (any, error) { return env, nil })
	if err != nil {
		return exprvalue.Value{}, historyExpressionError(err, field)
	}
	return value, nil
}

func (e *historyEvaluator) selectValue(
	program *exprvalue.Program,
	row EntityStateHistoryEntry,
) (exprvalue.Value, error) {
	if !jsontext.Value(row.Value).IsValid() {
		return exprvalue.Value{}, fmt.Errorf("corrupt retained history value")
	}
	value, err := exprvalue.DecodeValue(json.RawMessage(row.Value))
	if err != nil {
		return exprvalue.Value{}, historyExpressionError(err, "select_expr")
	}
	var source any
	if row.SourceUpdatedAt != nil {
		source = row.SourceUpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	sample := map[string]any{
		historyValueRoot:      value.Data,
		"observation_id":      string(row.ObservationID),
		"observed_at":         row.ObservedAt.UTC().Format(time.RFC3339Nano),
		"adapter_received_at": row.AdapterReceivedAt.UTC().Format(time.RFC3339Nano),
		"source_updated_at":   source,
	}
	return e.run(program, "select_expr", map[string]any{"sample": sample})
}

func (e *historyEvaluator) predicate(program *exprvalue.Program, field string, value exprvalue.Value) (bool, error) {
	// Own each value independently; the envelope is not charged against its limit.
	owned, err := exprvalue.OwnValue(value.Data)
	if err != nil {
		return false, historyExpressionError(err, field)
	}
	result, err := e.run(program, field, map[string]any{historyValueRoot: owned.Data})
	if err != nil {
		return false, err
	}
	match, ok := result.Data.(bool)
	if !ok {
		return false, historyError("expression_type", field)
	}
	return match, nil
}
