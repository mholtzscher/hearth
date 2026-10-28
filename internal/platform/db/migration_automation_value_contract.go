package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type valueContractCandidate struct {
	run, step, kind, evidence, definition, failure string
}

const automationValueContractMigrationVersion = 24
const assignmentField = "assignments"
const chooseKind = "choose"

//nolint:gocognit,sqlclosecheck // Each query is explicitly closed before any update.
func migrateAutomationValueContract(ctx context.Context, tx *sql.Tx) error {
	queries := []string{
		`SELECT h.id, '', '', h.value_initialization_json, COALESCE(r.definition_json,''), h.value_initialization_json -> '$.failure' FROM automation_history h LEFT JOIN automation_definition_revisions r ON r.automation_id=h.automation_id AND r.revision=h.definition_revision WHERE json_type(h.value_initialization_json,'$.failure') IS NOT NULL`,
		`SELECT e.run_id, e.step_id, e.kind, e.evidence_json, COALESCE(r.definition_json,''), e.evidence_json -> '$.value_failure' FROM automation_step_executions e JOIN automation_history h ON h.id=e.run_id LEFT JOIN automation_definition_revisions r ON r.automation_id=h.automation_id AND r.revision=h.definition_revision WHERE json_type(e.evidence_json,'$.value_failure') IS NOT NULL`,
	}
	var candidates []valueContractCandidate
	for _, query := range queries {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c valueContractCandidate
			if err = rows.Scan(&c.run, &c.step, &c.kind, &c.evidence, &c.definition, &c.failure); err != nil {
				_ = rows.Close()
				return err
			}
			candidates = append(candidates, c)
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
	}
	for _, c := range candidates {
		converted, err := convertValueContractFailure(c)
		if err != nil {
			return fmt.Errorf("migrate value failure for run %s step %s: %w", c.run, c.step, err)
		}
		if c.step == "" {
			_, err = tx.ExecContext(
				ctx,
				`UPDATE automation_history SET value_initialization_json=json_set(value_initialization_json,'$.failure',json(?)) WHERE id=?`,
				converted,
				c.run,
			)
		} else {
			_, err = tx.ExecContext(
				ctx,
				`UPDATE automation_step_executions SET evidence_json=json_set(evidence_json,'$.value_failure',json(?)) WHERE run_id=? AND step_id=?`,
				converted,
				c.run,
				c.step,
			)
		}
		if err != nil {
			return err
		}
	}
	script, err := migrationFiles.ReadFile("migrations/00024_automation_value_contract.sql")
	if err != nil {
		return err
	}
	up, _, found := strings.Cut(string(script), "-- +goose Down")
	if !found {
		return errors.New("invalid value contract migration script")
	}
	_, err = tx.ExecContext(ctx, up)
	return err
}

func refuseAutomationValueContractDown(context.Context, *sql.Tx) error {
	return errors.New(
		"restore the pre-upgrade backup with its matching binary; value failures have no lossless Down migration",
	)
}

func valueObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("expected object")
	}
	return object, nil
}
func valueString(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
func valueArray(raw json.RawMessage) ([]json.RawMessage, error) {
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil || a == nil {
		return nil, errors.New("expected array")
	}
	return a, nil
}
func valueIndex(s string, n int) (int, error) {
	i, err := strconv.Atoi(s)
	if err != nil || i < 0 || i >= n || strconv.Itoa(i) != s {
		return 0, errors.New("invalid array index")
	}
	return i, nil
}

// valueNode walks the old definition-relative JSON pointer without decoding values.
//
//nolint:nestif // Objects and arrays require different traversal checks.
func valueNode(raw json.RawMessage, parts []string) (json.RawMessage, error) {
	for _, p := range parts {
		if strings.Contains(p, "~") || p == "" {
			return nil, errors.New("invalid path token")
		}
		if len(raw) > 0 && raw[0] == '[' {
			a, err := valueArray(raw)
			if err != nil {
				return nil, err
			}
			i, err := valueIndex(p, len(a))
			if err != nil {
				return nil, err
			}
			raw = a[i]
		} else {
			o, err := valueObject(raw)
			if err != nil {
				return nil, err
			}
			var ok bool
			raw, ok = o[p]
			if !ok {
				return nil, errors.New("path not found")
			}
		}
	}
	return raw, nil
}

// stepPathEnd accepts only actual nested Step sequences, not arbitrary vendor JSON.
func stepPathEnd(definition json.RawMessage, parts []string, id, kind string) (int, error) {
	if len(parts) < 2 || parts[0] != "steps" {
		return 0, errors.New("invalid step path")
	}
	end := 2
	for {
		raw, err := valueNode(definition, parts[:end])
		if err != nil {
			return 0, err
		}
		step, err := valueObject(raw)
		if err != nil {
			return 0, err
		}
		stepKind := valueString(step["kind"])
		if valueString(step["id"]) == id && stepKind == kind {
			return end, nil
		}
		switch {
		case end+2 <= len(parts) && stepKind == "if" && (parts[end] == "then" || parts[end] == "else"):
			end += 2
		case end+2 <= len(parts) && stepKind == chooseKind && parts[end] == "default":
			end += 2
		case end+4 <= len(parts) && stepKind == chooseKind && parts[end] == "branches" && parts[end+2] == "steps":
			end += 4
		default:
			return 0, errors.New("step path does not match retained revision")
		}
	}
}

//nolint:gocognit,gocyclo,cyclop,funlen,nestif,mnd // The old path grammar has distinct kind-specific forms.
func convertValueContractFailure(c valueContractCandidate) (string, error) {
	fail, err := valueObject(json.RawMessage(c.failure))
	if err != nil {
		return "", err
	}
	path := valueString(fail["path"])
	code := valueString(fail["failure_code"])
	if path == "" || code == "" || c.definition == "" {
		return "", errors.New("missing path, failure code, or retained revision")
	}
	var field, condition string
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if !strings.HasPrefix(path, "/") {
		return "", errors.New("invalid path")
	}
	if c.step == "" {
		switch {
		case path == "/trigger":
			field = "trigger"
		case path == "/variables":
			field = "variables"
		case len(parts) == 2 && (parts[0] == "variables" || parts[0] == "variables_expr"):
			if _, err = valueNode(json.RawMessage(c.definition), parts); err != nil {
				return "", err
			}
			field = "variables." + parts[1]
		default:
			return "", errors.New("invalid initialization path")
		}
	} else {
		stepEnd, pathErr := stepPathEnd(json.RawMessage(c.definition), parts, c.step, c.kind)
		if pathErr != nil {
			return "", pathErr
		}
		suffix := parts[stepEnd:]
		switch c.kind {
		case "if", chooseKind:
			if len(suffix) == 0 && code == "run_data_limit" {
				break
			}
			root := append([]string{}, parts[:stepEnd]...)
			if c.kind == "if" {
				root = append(root, "conditions")
			} else {
				ev, e := valueObject(json.RawMessage(c.evidence))
				if e != nil {
					return "", e
				}
				evaluations, e := valueArray(ev["evaluations"])
				if e != nil {
					return "", e
				}
				stepRaw, e := valueNode(json.RawMessage(c.definition), parts[:stepEnd])
				if e != nil {
					return "", e
				}
				step, e := valueObject(stepRaw)
				if e != nil {
					return "", e
				}
				branches, e := valueArray(step["branches"])
				if e != nil || len(evaluations) >= len(branches) {
					return "", errors.New("invalid choose prefix")
				}
				for i, v := range evaluations {
					evaluation, evaluationErr := valueObject(v)
					if evaluationErr != nil {
						return "", evaluationErr
					}
					branch, branchErr := valueObject(branches[i])
					if branchErr != nil || valueString(evaluation["branch_id"]) != valueString(branch["id"]) {
						return "", errors.New("choose prefix disagrees with revision")
					}
				}
				root = append(root, "branches", strconv.Itoa(len(evaluations)), "conditions")
			}
			if len(parts) <= len(root) || strings.Join(parts[:len(root)], "/") != strings.Join(root, "/") ||
				parts[len(parts)-1] != "expr" {
				return "", errors.New("invalid branch path")
			}
			rest := parts[len(root) : len(parts)-1]
			conditionPath := append([]string{}, root...)
			for len(rest) > 0 {
				parentRaw, parentErr := valueNode(json.RawMessage(c.definition), conditionPath)
				if parentErr != nil {
					return "", parentErr
				}
				parent, parentErr := valueObject(parentRaw)
				if parentErr != nil {
					return "", parentErr
				}
				parentKind := valueString(parent["kind"])
				switch {
				case rest[0] == "child" && parentKind == "not":
					conditionPath = append(conditionPath, rest[0])
					rest = rest[1:]
				case rest[0] == "children" && len(rest) >= 2 && (parentKind == "all" || parentKind == "any"):
					conditionPath = append(conditionPath, rest[:2]...)
					rest = rest[2:]
				default:
					return "", errors.New("invalid condition path")
				}
			}
			raw, e := valueNode(json.RawMessage(c.definition), parts[:len(parts)-1])
			if e != nil {
				return "", e
			}
			node, e := valueObject(raw)
			if e != nil || valueString(node["kind"]) != "expression" || len(node["expr"]) == 0 {
				return "", errors.New("invalid expression condition")
			}
			condition = valueString(node["id"])
			if condition == "" {
				return "", errors.New("missing condition id")
			}
		case "command", "function", "notification", "read_state", "set_variables":
			if len(suffix) == 0 {
				break
			}
			allowed := map[string]string{"command": "parameters", "function": "arguments", "set_variables": assignmentField}[c.kind]
			name := strings.TrimSuffix(suffix[0], "_expr")
			if c.kind == "notification" {
				if name != "title" && name != "message" {
					return "", errors.New("invalid notification field")
				}
			} else if name != allowed || allowed == "" {
				return "", errors.New("invalid step field")
			}
			if len(suffix) > 1 && (name != assignmentField || len(suffix) != 2) {
				return "", errors.New("invalid step field path")
			}
			if len(suffix) == 2 {
				if _, err = valueNode(json.RawMessage(c.definition), parts); err != nil {
					return "", err
				}
				field = name + "." + suffix[1]
			} else {
				if name != assignmentField {
					if _, err = valueNode(json.RawMessage(c.definition), parts); err != nil {
						return "", err
					}
				}
				field = name
			}
		default:
			return "", errors.New("unsupported failure step")
		}
	}
	delete(fail, "path")
	delete(fail, "source_start")
	delete(fail, "source_end")
	if field != "" {
		fail["field"], _ = json.Marshal(field)
	}
	if condition != "" {
		fail["condition_id"], _ = json.Marshal(condition)
	}
	out, err := json.Marshal(fail)
	return string(out), err
}
