package inspector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/privacy"
	"hgdb_monitor/core/internal/sqlsafe"
)

const maxExplainResultBytes = 256 * 1024

type ExplainResult struct {
	Plan any `json:"plan"`
}

func Explain(ctx context.Context, config datasource.TestConfig, inputSQL string) (ExplainResult, error) {
	normalized, err := sqlsafe.NormalizeExplainInput(inputSQL)
	if err != nil {
		return ExplainResult{}, &datasource.PublicError{Code: "INVALID_READONLY_SQL", Message: err.Error()}
	}
	conn, timeout, err := datasource.Open(ctx, config)
	if err != nil {
		return ExplainResult{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tx, err := conn.BeginTx(queryCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ExplainResult{}, &datasource.PublicError{Code: "READ_ONLY_SETUP_FAILED", Message: "could not start a read-only database transaction"}
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(queryCtx, `SET LOCAL statement_timeout = '10s'`); err != nil {
		return ExplainResult{}, datasource.SanitizeProbeError(err)
	}
	if _, err := tx.Exec(queryCtx, `SET LOCAL lock_timeout = '1s'`); err != nil {
		return ExplainResult{}, datasource.SanitizeProbeError(err)
	}
	var encoded string
	statement := `EXPLAIN (FORMAT JSON, ANALYZE FALSE, COSTS TRUE, VERBOSE FALSE, SETTINGS FALSE, SUMMARY TRUE) ` + normalized
	if err := tx.QueryRow(queryCtx, statement).Scan(&encoded); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ExplainResult{}, datasource.SanitizeProbeError(err)
		}
		return ExplainResult{}, &datasource.PublicError{Code: "EXPLAIN_FAILED", Message: "query plan could not be generated"}
	}
	var plan any
	if err := json.Unmarshal([]byte(encoded), &plan); err != nil {
		return ExplainResult{}, fmt.Errorf("decode explain plan: %w", err)
	}
	if planDepth(plan, 0) > 64 {
		return ExplainResult{}, &datasource.PublicError{Code: "RESULT_LIMIT_EXCEEDED", Message: "query plan is too deep to return"}
	}
	plan = sanitizePlan(plan, "")
	redacted, err := json.Marshal(plan)
	if err != nil {
		return ExplainResult{}, fmt.Errorf("encode explain plan: %w", err)
	}
	if len(redacted) > maxExplainResultBytes {
		return ExplainResult{}, &datasource.PublicError{Code: "RESULT_LIMIT_EXCEEDED", Message: "query plan is too large to return"}
	}
	return ExplainResult{Plan: plan}, nil
}

func planDepth(value any, depth int) int {
	maximum := depth
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			maximum = max(maximum, planDepth(child, depth+1))
		}
	case []any:
		for _, child := range typed {
			maximum = max(maximum, planDepth(child, depth+1))
		}
	}
	return maximum
}

func sanitizePlan(value any, key string) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for childKey, child := range typed {
			result[childKey] = sanitizePlan(child, childKey)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = sanitizePlan(child, key)
		}
		return result
	case string:
		switch key {
		case "Relation Name", "Schema", "Index Name", "Alias", "Node Type", "Join Type", "Strategy", "Operation":
			return typed
		default:
			return privacy.RedactSQL(typed)
		}
	default:
		return value
	}
}
