package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// RecordRunData commits a pure Step's evidence before the executor changes its
// private context. Effect inputs use their existing reservation transaction.
func (repo *AutomationRepository) RecordRunData(
	ctx context.Context,
	id automations.RunID,
	record automations.RunDataRecord,
) error {
	return repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		run, err := startableEvidenceParent(ctx, q, id)
		if err != nil {
			return err
		}
		switch record.Body.(type) {
		case automations.VariablesInitialized, automations.VariablesAssigned, automations.StateCaptured:
		case automations.FunctionOutputCaptured, automations.InputsResolved, automations.RunDataFailed:
			return dataCorruption("operation does not accept this record kind")
		default:
			return dataCorruption("operation does not accept this record kind")
		}
		return appendRunData(ctx, q, run, record, false)
	})
}

// FailRunData appends the reserved failure record and terminalizes the Run in one
// transaction. It never fabricates an effect reservation or provider outcome.
func (repo *AutomationRepository) FailRunData(
	ctx context.Context,
	id automations.RunID,
	record automations.RunDataRecord,
) error {
	return repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		run, err := startableEvidenceParent(ctx, q, id)
		if err != nil {
			return err
		}
		body, ok := record.Body.(automations.RunDataFailed)
		if !ok {
			return dataCorruption("failure operation requires failure evidence")
		}
		if err = appendRunData(ctx, q, run, record, false); err != nil {
			return err
		}
		return failDataParent(ctx, q, id, record.RecordedAt, body.FailureCode)
	})
}

func appendRunData(
	ctx context.Context,
	q *dbsqlc.Queries,
	run automations.Run,
	record automations.RunDataRecord,
	pendingOutput bool,
) error {
	if run.DataEvidence != automations.RunDataRecorded {
		return dataCorruption("legacy parent cannot acquire value evidence")
	}
	if record.Sequence != len(run.Data) {
		return dataCorruption("sequence is not appendable")
	}
	if err := validateRunDataPhase(run, record, pendingOutput); err != nil {
		return err
	}
	raw, err := automations.EncodeRunDataRecord(record)
	if err != nil {
		return err
	}
	countLimit, byteLimit := automations.RunDataMaxRecords-1, automations.RunDataMaxBytes-automations.RunDataFailureReserveBytes
	if _, failure := record.Body.(automations.RunDataFailed); failure {
		countLimit, byteLimit = automations.RunDataMaxRecords, automations.RunDataMaxBytes
		if len(raw) > automations.RunDataFailureReserveBytes {
			return dataCorruption("failure exceeds reserved capacity")
		}
	}
	total := len(raw)
	for _, previous := range run.Data {
		encoded, encodeErr := automations.EncodeRunDataRecord(previous)
		if encodeErr != nil {
			return encodeErr
		}
		total += len(encoded)
	}
	if record.Sequence >= countLimit || total > byteLimit {
		return &exprvalue.Failure{Code: "run_data_limit"}
	}
	kind, payload, err := runDataPayload(raw)
	if err != nil {
		return err
	}
	step := sql.NullString{}
	if record.StepID != nil {
		step = sql.NullString{String: string(*record.StepID), Valid: true}
	}
	return q.CreateRunData(
		ctx,
		dbsqlc.CreateRunDataParams{
			RunID:       string(run.ID),
			Sequence:    int64(record.Sequence),
			StepID:      step,
			Kind:        kind,
			RecordedAt:  encodeAutomationTimestamp(record.RecordedAt),
			PayloadJson: string(payload),
		},
	)
}

// The canonical encoder owns public shape; SQL alone owns the envelope. Decode
// into RawMessages so no provider number passes through a floating-point value.
func runDataPayload(raw json.RawMessage) (string, json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", nil, err
	}
	var kind string
	if err := json.Unmarshal(fields["kind"], &kind); err != nil {
		return "", nil, err
	}
	for _, key := range []string{"sequence", "step_id", "kind", "recorded_at"} {
		delete(fields, key)
	}
	payload, err := json.Marshal(fields)
	return kind, payload, err
}

//nolint:gocognit // Reconstructing the canonical envelope validates every independently stored SQL field before decoding.
func runDataRecords(ctx context.Context, q *dbsqlc.Queries, run automations.Run) ([]automations.RunDataRecord, error) {
	rows, err := q.ListRunData(ctx, dbsqlc.ListRunDataParams{RunID: string(run.ID)})
	if err != nil {
		return nil, err
	}
	if run.DataEvidence != automations.RunDataRecorded && run.DataEvidence != automations.RunDataLegacyUnavailable {
		return nil, dataCorruption("unknown provenance")
	}
	if run.DataEvidence == automations.RunDataLegacyUnavailable && len(rows) != 0 {
		return nil, dataCorruption("legacy parent has value evidence")
	}
	result := make([]automations.RunDataRecord, 0, len(rows))
	total := 0
	for index, row := range rows {
		if row.RunID != string(run.ID) || row.Sequence != int64(index) {
			return nil, dataCorruption("retained identity or sequence disagrees")
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal([]byte(row.PayloadJson), &fields); err != nil || fields == nil {
			return nil, dataCorruption("invalid payload")
		}
		for _, key := range []string{"sequence", "step_id", "kind", "recorded_at"} {
			if _, exists := fields[key]; exists {
				return nil, dataCorruption("payload duplicates envelope")
			}
		}
		fields["sequence"] = json.RawMessage(strconv.FormatInt(row.Sequence, 10))
		fields["kind"], err = json.Marshal(row.Kind)
		if err != nil {
			return nil, err
		}
		fields["recorded_at"], err = json.Marshal(row.RecordedAt)
		if err != nil {
			return nil, err
		}
		if row.StepID.Valid {
			fields["step_id"], err = json.Marshal(row.StepID.String)
			if err != nil {
				return nil, err
			}
		}
		raw, encodeErr := json.Marshal(fields)
		if encodeErr != nil {
			return nil, encodeErr
		}
		record, decodeErr := automations.DecodeRunDataRecord(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		canonical, encodeErr := automations.EncodeRunDataRecord(record)
		if encodeErr != nil {
			return nil, encodeErr
		}
		total += len(canonical)
		if total > automations.RunDataMaxBytes || index >= automations.RunDataMaxRecords {
			return nil, dataCorruption("retained journal exceeds capacity")
		}
		result = append(result, record)
	}
	return result, nil
}

func validateRunDataPhase(run automations.Run, record automations.RunDataRecord, pendingOutput bool) error {
	if len(run.Data) == 0 {
		if record.StepID != nil {
			return dataCorruption("initialization must precede Step values")
		}
	} else {
		if _, failed := run.Data[len(run.Data)-1].Body.(automations.RunDataFailed); failed {
			return dataCorruption("failure is terminal")
		}
		if record.StepID == nil {
			return dataCorruption("initialization is insert-once")
		}
	}
	if err := automations.ValidateRunDataWithPreparedSnapshot(record, run.Snapshot); err != nil {
		return err
	}
	for _, previous := range run.Data {
		if sameDataStep(previous.StepID, record.StepID) &&
			reflect.TypeOf(previous.Body) == reflect.TypeOf(record.Body) {
			return dataCorruption("phase is insert-once")
		}
	}
	if _, output := record.Body.(automations.FunctionOutputCaptured); output {
		return validateFunctionOutputPhase(run, *record.StepID, pendingOutput)
	}
	return nil
}

func validateFunctionOutputPhase(run automations.Run, id automations.StepID, pendingOutput bool) error {
	if !hasRunDataBody[automations.InputsResolved](run, id) {
		return dataCorruption("output must follow resolved inputs")
	}
	for _, function := range run.Functions {
		if function.StepID == id &&
			(automations.FunctionStateStatus(function.State) == functionStatusSucceeded ||
				(pendingOutput && automations.FunctionStateStatus(function.State) == functionStatusRunning)) {
			return nil
		}
	}
	return dataCorruption("output has no successful invocation phase")
}

func sameDataStep(left, right *automations.StepID) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func dataCorruption(message string) error {
	return fmt.Errorf("%w: Run data %s", automations.ErrInvalidAutomation, message)
}

func failDataParent(ctx context.Context, q *dbsqlc.Queries, id automations.RunID, at time.Time, code string) error {
	updated, err := q.CompleteRun(
		ctx,
		dbsqlc.CompleteRunParams{
			ID:             string(id),
			RunStatus:      sql.NullString{String: string(automations.RunFailed), Valid: true},
			RunFailureCode: sql.NullString{String: code, Valid: true},
			RunCompletedAt: sql.NullString{String: encodeAutomationTimestamp(at), Valid: true},
		},
	)
	if err != nil {
		return err
	}
	if updated != 1 {
		return dataCorruption("parent is not completable")
	}
	return nil
}

func requireDataInitialized(run automations.Run) error {
	if run.DataEvidence == automations.RunDataRecorded {
		if len(run.Data) == 0 {
			return dataCorruption("execution requires initialization")
		}
		if _, ok := run.Data[0].Body.(automations.VariablesInitialized); !ok {
			return dataCorruption("execution has no successful initialization")
		}
	}
	return nil
}

func validateRetainedRunData(run automations.Run) error {
	if run.DataEvidence == automations.RunDataLegacyUnavailable {
		return nil
	}
	prefix := run
	prefix.Data = nil
	for _, record := range run.Data {
		if err := validateRunDataPhase(prefix, record, false); err != nil {
			return err
		}
		prefix.Data = append(prefix.Data, record)
	}
	if automations.RunStateStatus(run.State) == automations.RunSucceeded {
		if err := requireDataInitialized(run); err != nil {
			return err
		}
	}
	if len(run.Data) > 0 {
		if failure, failed := run.Data[len(run.Data)-1].Body.(automations.RunDataFailed); failed {
			_, reason := automations.RunStateCompletion(run.State)
			if automations.RunStateStatus(run.State) != automations.RunFailed || reason == nil ||
				*reason != failure.FailureCode {
				return dataCorruption("failure disagrees with terminal parent")
			}
		}
	}
	return validateRetainedEffectData(run)
}

func validateRetainedEffectData(run automations.Run) error {
	reservedSteps := make(map[automations.StepID]bool)
	for _, step := range run.Steps {
		reserved := hasCommandReservation(step.State)
		reservedSteps[step.StepID] = reserved
		if reserved && !hasRunDataBody[automations.InputsResolved](run, step.StepID) {
			return dataCorruption("Command reservation has no inputs")
		}
	}
	for _, notification := range run.Notifications {
		reservedSteps[notification.StepID] = true
		if !hasRunDataBody[automations.InputsResolved](run, notification.StepID) {
			return dataCorruption("notification reservation has no inputs")
		}
	}
	for _, function := range run.Functions {
		reservedSteps[function.StepID] = true
		if !hasRunDataBody[automations.InputsResolved](run, function.StepID) {
			return dataCorruption("Function reservation has no inputs")
		}
		if automations.FunctionStateStatus(function.State) == functionStatusSucceeded {
			if err := validateRetainedFunctionData(run, function); err != nil {
				return err
			}
		}
	}
	for _, record := range run.Data {
		if _, inputs := record.Body.(automations.InputsResolved); inputs && !reservedSteps[*record.StepID] {
			return dataCorruption("inputs have no effect reservation")
		}
	}
	return nil
}

// Retained completion data obeys the same output/failure contract as a write,
// including the restricted failure classes after verified provider success.
func validateRetainedFunctionData(run automations.Run, function automations.FunctionExecution) error {
	var data *automations.RunDataRecord
	for index := range run.Data {
		record := &run.Data[index]
		if record.StepID == nil || *record.StepID != function.StepID {
			continue
		}
		switch record.Body.(type) {
		case automations.FunctionOutputCaptured, automations.RunDataFailed:
			if data != nil {
				return dataCorruption("Function has multiple completion data records")
			}
			data = record
		case automations.VariablesInitialized,
			automations.VariablesAssigned,
			automations.StateCaptured,
			automations.InputsResolved:
		}
	}
	if data == nil {
		return dataCorruption("successful Function has no retained completion data")
	}
	return automations.ValidateFunctionCompletion(
		automations.FunctionCompletion{
			RunID:        run.ID,
			StepID:       function.StepID,
			InvocationID: function.InvocationID,
			State:        function.State,
			Data:         data,
		},
	)
}

func hasCommandReservation(state automations.StepAttemptState) bool {
	switch state := state.(type) {
	case automations.RunningStep:
		return true
	case automations.CompletedStep:
		return state.Reservation != nil
	case automations.NotAttemptedStep:
		return false
	default:
		return false
	}
}

func hasRunDataBody[T automations.RunDataBody](run automations.Run, id automations.StepID) bool {
	for _, record := range run.Data {
		if record.StepID != nil && *record.StepID == id {
			if _, ok := record.Body.(T); ok {
				return true
			}
		}
	}
	return false
}
