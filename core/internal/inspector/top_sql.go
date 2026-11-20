package inspector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/privacy"
)

type TopSQLOrder string

const (
	TopSQLTotalTime TopSQLOrder = "total_time"
	TopSQLMeanTime  TopSQLOrder = "mean_time"
	TopSQLCalls     TopSQLOrder = "calls"
	TopSQLRows      TopSQLOrder = "rows"
)

type TopSQLItem struct {
	QueryID           string  `json:"queryId"`
	DatabaseOID       uint32  `json:"databaseOid"`
	UserOID           uint32  `json:"userOid"`
	SQL               string  `json:"sql"`
	Calls             int64   `json:"calls"`
	TotalTimeMS       float64 `json:"totalTimeMs"`
	MeanTimeMS        float64 `json:"meanTimeMs"`
	Rows              int64   `json:"rows"`
	SharedBlocksHit   int64   `json:"sharedBlocksHit"`
	SharedBlocksRead  int64   `json:"sharedBlocksRead"`
	TempBlocksRead    int64   `json:"tempBlocksRead"`
	TempBlocksWritten int64   `json:"tempBlocksWritten"`
	WALBytes          int64   `json:"walBytes"`
}

type TopSQLResult struct {
	Items      []TopSQLItem `json:"items"`
	StatsReset *time.Time   `json:"statsReset,omitempty"`
}

func TopSQL(ctx context.Context, config datasource.TestConfig, order TopSQLOrder, limit int) (TopSQLResult, error) {
	orderColumn, ok := map[TopSQLOrder]string{
		TopSQLTotalTime: "total_exec_time", TopSQLMeanTime: "mean_exec_time",
		TopSQLCalls: "calls", TopSQLRows: "rows",
	}[order]
	if !ok {
		return TopSQLResult{}, &datasource.PublicError{Code: "INVALID_TOP_SQL_ORDER", Message: "unsupported Top SQL ordering"}
	}
	if limit <= 0 || limit > 20 {
		return TopSQLResult{}, &datasource.PublicError{Code: "INVALID_LIMIT", Message: "Top SQL limit must be between 1 and 20"}
	}
	conn, timeout, err := datasource.Open(ctx, config)
	if err != nil {
		return TopSQLResult{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tx, err := conn.BeginTx(queryCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return TopSQLResult{}, &datasource.PublicError{Code: "READ_ONLY_SETUP_FAILED", Message: "could not start a read-only database transaction"}
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var schema string
	err = tx.QueryRow(queryCtx, `SELECT n.nspname FROM pg_catalog.pg_extension e JOIN pg_catalog.pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_stat_statements'`).Scan(&schema)
	if err == pgx.ErrNoRows {
		return TopSQLResult{}, &datasource.PublicError{Code: "FEATURE_UNAVAILABLE", Message: "pg_stat_statements is not installed"}
	}
	if err != nil {
		return TopSQLResult{}, datasource.SanitizeProbeError(err)
	}
	view := extensionRelation(schema, "pg_stat_statements")
	infoView := extensionRelation(schema, "pg_stat_statements_info")
	query := fmt.Sprintf(`SELECT queryid::text, dbid::oid, userid::oid, coalesce(query,''), calls::bigint,
		total_exec_time::double precision, mean_exec_time::double precision, rows::bigint,
		shared_blks_hit::bigint, shared_blks_read::bigint, temp_blks_read::bigint, temp_blks_written::bigint,
		coalesce(wal_bytes,0)::bigint FROM %s ORDER BY %s DESC NULLS LAST LIMIT $1`, view, orderColumn)
	rows, err := tx.Query(queryCtx, query, limit)
	if err != nil {
		return TopSQLResult{}, datasource.SanitizeProbeError(err)
	}
	defer rows.Close()
	result := TopSQLResult{Items: make([]TopSQLItem, 0, limit)}
	for rows.Next() {
		var item TopSQLItem
		var rawSQL string
		if err := rows.Scan(&item.QueryID, &item.DatabaseOID, &item.UserOID, &rawSQL, &item.Calls,
			&item.TotalTimeMS, &item.MeanTimeMS, &item.Rows, &item.SharedBlocksHit, &item.SharedBlocksRead,
			&item.TempBlocksRead, &item.TempBlocksWritten, &item.WALBytes); err != nil {
			return TopSQLResult{}, datasource.SanitizeProbeError(err)
		}
		item.SQL = privacy.RedactSQLLimit(rawSQL, 500)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return TopSQLResult{}, datasource.SanitizeProbeError(err)
	}
	var reset time.Time
	if err := tx.QueryRow(queryCtx, fmt.Sprintf(`SELECT stats_reset FROM %s`, infoView)).Scan(&reset); err == nil {
		value := reset.UTC()
		result.StatsReset = &value
	}
	return result, nil
}

func extensionRelation(schema, name string) string {
	return pgx.Identifier{schema, name}.Sanitize()
}
