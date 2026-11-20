package inspector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/privacy"
)

type ActivityKind string

const (
	ActivityActive          ActivityKind = "active"
	ActivityWaiting         ActivityKind = "waiting"
	ActivityLongQuery       ActivityKind = "long_query"
	ActivityLongTransaction ActivityKind = "long_transaction"
	ActivityIdleTransaction ActivityKind = "idle_in_transaction"
)

type Activity struct {
	PID                   int32      `json:"pid"`
	Database              string     `json:"database,omitempty"`
	User                  string     `json:"user,omitempty"`
	ApplicationName       string     `json:"applicationName,omitempty"`
	ClientAddress         string     `json:"clientAddress,omitempty"`
	State                 string     `json:"state"`
	WaitEventType         string     `json:"waitEventType,omitempty"`
	WaitEvent             string     `json:"waitEvent,omitempty"`
	QueryAgeSeconds       float64    `json:"queryAgeSeconds"`
	TransactionAgeSeconds float64    `json:"transactionAgeSeconds"`
	SQL                   string     `json:"sql"`
	BackendStart          *time.Time `json:"backendStart,omitempty"`
	TransactionStart      *time.Time `json:"transactionStart,omitempty"`
	QueryStart            *time.Time `json:"queryStart,omitempty"`
}

type LockWait struct {
	BlockedPID       int32   `json:"blockedPid"`
	BlockingPID      int32   `json:"blockingPid"`
	BlockerType      string  `json:"blockerType"`
	BlockerIdentity  string  `json:"-"`
	LockType         string  `json:"lockType,omitempty"`
	RequestedMode    string  `json:"requestedMode,omitempty"`
	SchemaName       string  `json:"schemaName,omitempty"`
	RelationName     string  `json:"relationName,omitempty"`
	QueryAgeSeconds  float64 `json:"queryAgeSeconds"`
	BlockedSQL       string  `json:"blockedSql"`
	BlockingSQL      string  `json:"blockingSql"`
	BlockedDatabase  string  `json:"blockedDatabase,omitempty"`
	BlockedUser      string  `json:"blockedUser,omitempty"`
	BlockingDatabase string  `json:"blockingDatabase,omitempty"`
	BlockingUser     string  `json:"blockingUser,omitempty"`
}

func Activities(ctx context.Context, config datasource.TestConfig, kind ActivityKind, limit int) ([]Activity, error) {
	return ActivitiesWithMinimum(ctx, config, kind, 0, limit)
}

func ActivitiesWithMinimum(ctx context.Context, config datasource.TestConfig, kind ActivityKind, minimumSeconds, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 50 {
		return nil, &datasource.PublicError{Code: "INVALID_LIMIT", Message: "activity limit must be between 1 and 50"}
	}
	if minimumSeconds < 0 || minimumSeconds > 86400 {
		return nil, &datasource.PublicError{Code: "INVALID_DURATION", Message: "minimum duration must be between 0 and 86400 seconds"}
	}
	if minimumSeconds == 0 {
		switch kind {
		case ActivityLongQuery:
			minimumSeconds = 60
		case ActivityLongTransaction, ActivityIdleTransaction:
			minimumSeconds = 300
		}
	}
	query, err := buildActivityQuery(kind, minimumSeconds)
	if err != nil {
		return nil, err
	}
	conn, timeout, err := datasource.Open(ctx, config)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tx, err := conn.BeginTx(queryCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, &datasource.PublicError{Code: "READ_ONLY_SETUP_FAILED", Message: "could not start a read-only database transaction"}
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	rows, err := tx.Query(queryCtx, query, limit, minimumSeconds, string(kind))
	if err != nil {
		return nil, datasource.SanitizeProbeError(err)
	}
	defer rows.Close()
	result := make([]Activity, 0)
	for rows.Next() {
		var activity Activity
		var rawSQL string
		if err := rows.Scan(&activity.PID, &activity.Database, &activity.User, &activity.ApplicationName, &activity.ClientAddress,
			&activity.State, &activity.WaitEventType, &activity.WaitEvent, &activity.QueryAgeSeconds,
			&activity.TransactionAgeSeconds, &rawSQL, &activity.BackendStart,
			&activity.TransactionStart, &activity.QueryStart); err != nil {
			return nil, datasource.SanitizeProbeError(err)
		}
		activity.SQL = privacy.RedactSQL(rawSQL)
		result = append(result, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, datasource.SanitizeProbeError(err)
	}
	return result, nil
}

func buildActivityQuery(kind ActivityKind, minimumSeconds int) (string, error) {
	predicate, err := activityPredicate(kind)
	if err != nil {
		return "", err
	}
	if minimumSeconds > 0 {
		switch kind {
		case ActivityLongTransaction:
			predicate += ` AND extract(epoch FROM clock_timestamp() - xact_start) >= $2`
		case ActivityIdleTransaction:
			predicate += ` AND extract(epoch FROM clock_timestamp() - state_change) >= $2`
		default:
			predicate += ` AND query_start IS NOT NULL AND extract(epoch FROM clock_timestamp() - query_start) >= $2`
		}
	}
	// Keep all three bind positions typed even when the selected activity kind
	// does not need a duration filter. PostgreSQL cannot infer a skipped $2
	// parameter when $1 and $3 are still referenced by the statement.
	predicate += ` AND $2::integer >= 0`
	return activityQueryPrefix + predicate + activityQuerySuffix, nil
}

const activityQueryPrefix = `
		SELECT pid, coalesce(datname, ''), coalesce(usename, ''), coalesce(application_name, ''), coalesce(client_addr::text, ''),
		       coalesce(state, ''), coalesce(wait_event_type, ''), coalesce(wait_event, ''),
		       CASE WHEN query_start IS NULL THEN 0 ELSE extract(epoch FROM clock_timestamp() - query_start) END,
		       CASE WHEN xact_start IS NULL THEN 0 ELSE extract(epoch FROM clock_timestamp() - xact_start) END,
		       coalesce(query, ''), backend_start, xact_start, query_start
		FROM pg_catalog.pg_stat_activity
		WHERE backend_type = 'client backend' AND pid <> pg_backend_pid() AND `

const activityQuerySuffix = `
		ORDER BY CASE WHEN $3::text = 'long_transaction' THEN xact_start WHEN $3::text = 'idle_in_transaction' THEN state_change ELSE query_start END ASC NULLS LAST
		LIMIT $1`

const lockWaitQuery = `
	SELECT blocked.pid, coalesce(blocker.pid, blockers.blocker_pid),
	       CASE WHEN blockers.blocker_pid = 0 THEN 'prepared_transaction' ELSE 'session' END,
	       coalesce(waiting_lock.blocker_identity, blockers.blocker_pid::text),
	       coalesce(waiting_lock.locktype, ''), coalesce(waiting_lock.mode, ''),
	       coalesce(namespace.nspname, ''), coalesce(relation.relname, ''),
	       extract(epoch FROM clock_timestamp() - coalesce(blocked.query_start, clock_timestamp())),
	       coalesce(blocked.query, ''), coalesce(blocker.query, ''),
	       coalesce(blocked.datname,''), coalesce(blocked.usename,''), coalesce(blocker.datname,''), coalesce(blocker.usename,'')
	FROM pg_catalog.pg_stat_activity blocked
	CROSS JOIN LATERAL (
		SELECT DISTINCT blocker_pid
		FROM unnest(pg_catalog.pg_blocking_pids(blocked.pid)) AS blocking_pid(blocker_pid)
	) blockers
	LEFT JOIN pg_catalog.pg_stat_activity blocker ON blocker.pid = blockers.blocker_pid
	LEFT JOIN LATERAL (
		SELECT waiting.locktype, waiting.mode, waiting.database, waiting.relation,
		       CASE WHEN blockers.blocker_pid = 0
		         THEN concat_ws(':', 'prepared', waiting.locktype, waiting.database, waiting.relation,
		                        waiting.page, waiting.tuple, waiting.virtualxid, waiting.transactionid,
		                        waiting.classid, waiting.objid, waiting.objsubid)
		         ELSE coalesce(holding.virtualtransaction, holding.transactionid::text,
		                concat_ws(':', holding.locktype, holding.database, holding.relation,
		                          holding.page, holding.tuple, holding.classid, holding.objid, holding.objsubid))
		       END AS blocker_identity
		FROM pg_catalog.pg_locks waiting
		JOIN pg_catalog.pg_locks holding
		  ON ((blockers.blocker_pid = 0 AND holding.pid IS NULL) OR holding.pid = blockers.blocker_pid)
		 AND holding.locktype = waiting.locktype
		 AND holding.database IS NOT DISTINCT FROM waiting.database
		 AND holding.relation IS NOT DISTINCT FROM waiting.relation
		 AND holding.page IS NOT DISTINCT FROM waiting.page
		 AND holding.tuple IS NOT DISTINCT FROM waiting.tuple
		 AND holding.virtualxid IS NOT DISTINCT FROM waiting.virtualxid
		 AND holding.transactionid IS NOT DISTINCT FROM waiting.transactionid
		 AND holding.classid IS NOT DISTINCT FROM waiting.classid
		 AND holding.objid IS NOT DISTINCT FROM waiting.objid
		 AND holding.objsubid IS NOT DISTINCT FROM waiting.objsubid
		WHERE waiting.pid = blocked.pid AND NOT waiting.granted
		ORDER BY waiting.locktype, waiting.mode
		LIMIT 1
	) waiting_lock ON true
	LEFT JOIN pg_catalog.pg_class relation
	  ON relation.oid = waiting_lock.relation
	 AND waiting_lock.database = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
	LEFT JOIN pg_catalog.pg_namespace namespace ON namespace.oid = relation.relnamespace
	WHERE blocked.backend_type = 'client backend'
	ORDER BY blocked.query_start NULLS LAST
	LIMIT $1`

func LockWaits(ctx context.Context, config datasource.TestConfig, limit int) ([]LockWait, error) {
	if limit <= 0 || limit > 50 {
		return nil, &datasource.PublicError{Code: "INVALID_LIMIT", Message: "lock wait limit must be between 1 and 50"}
	}
	conn, timeout, err := datasource.Open(ctx, config)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rows, err := conn.Query(queryCtx, lockWaitQuery, limit)
	if err != nil {
		return nil, datasource.SanitizeProbeError(err)
	}
	defer rows.Close()
	result := make([]LockWait, 0)
	for rows.Next() {
		var item LockWait
		var blockedSQL, blockingSQL string
		if err := rows.Scan(&item.BlockedPID, &item.BlockingPID, &item.BlockerType, &item.BlockerIdentity, &item.LockType, &item.RequestedMode,
			&item.SchemaName, &item.RelationName, &item.QueryAgeSeconds, &blockedSQL, &blockingSQL,
			&item.BlockedDatabase, &item.BlockedUser, &item.BlockingDatabase, &item.BlockingUser); err != nil {
			return nil, datasource.SanitizeProbeError(err)
		}
		item.BlockedSQL = privacy.RedactSQL(blockedSQL)
		item.BlockingSQL = privacy.RedactSQL(blockingSQL)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, datasource.SanitizeProbeError(err)
	}
	return result, nil
}

func activityPredicate(kind ActivityKind) (string, error) {
	switch kind {
	case ActivityActive:
		return `state = 'active'`, nil
	case ActivityWaiting:
		return `state = 'active' AND wait_event IS NOT NULL`, nil
	case ActivityLongQuery:
		return `state = 'active' AND query_start IS NOT NULL`, nil
	case ActivityLongTransaction:
		return `xact_start IS NOT NULL AND state <> 'idle'`, nil
	case ActivityIdleTransaction:
		return `state LIKE 'idle in transaction%' AND state_change IS NOT NULL`, nil
	default:
		return "", &datasource.PublicError{Code: "INVALID_ACTIVITY_KIND", Message: fmt.Sprintf("unsupported activity kind %q", kind)}
	}
}
