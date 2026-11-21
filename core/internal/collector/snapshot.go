package collector

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/model"
)

type Snapshot = model.Snapshot

func CollectSnapshot(ctx context.Context, config datasource.TestConfig) (Snapshot, error) {
	snapshot, err := CollectMetrics(ctx, config)
	if err != nil {
		return Snapshot{}, err
	}
	status, err := CollectStatus(ctx, config)
	if err != nil {
		return Snapshot{}, err
	}
	model.ApplyStatus(&snapshot, status)
	return snapshot, nil
}

func CollectMetrics(ctx context.Context, config datasource.TestConfig) (Snapshot, error) {
	conn, timeout, err := datasource.Open(ctx, config)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tx, err := conn.BeginTx(queryCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, &datasource.PublicError{Code: "READ_ONLY_SETUP_FAILED", Message: "could not start a read-only database transaction"}
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var snapshot Snapshot
	err = tx.QueryRow(queryCtx, snapshotSQL).Scan(
		&snapshot.SessionsTotal,
		&snapshot.SessionsActive,
		&snapshot.SessionsIdleTransaction,
		&snapshot.SessionsWaiting,
		&snapshot.LongQueries,
		&snapshot.LongTransactions,
		&snapshot.Commits,
		&snapshot.Rollbacks,
		&snapshot.BlocksHit,
		&snapshot.BlocksRead,
		&snapshot.RowsInserted,
		&snapshot.RowsUpdated,
		&snapshot.RowsDeleted,
		&snapshot.TempFiles,
		&snapshot.TempBytes,
		&snapshot.Deadlocks,
		&snapshot.LocksNotGranted,
		&snapshot.BlockedSessions,
		&snapshot.WALBytes,
		&snapshot.ReplicationConnections,
		&snapshot.MaxReplayLagBytes,
		&snapshot.MaxReplayLagBytesAvailable,
		&snapshot.MaxReplayLagSeconds,
		&snapshot.MaxReplayLagSecondsAvailable,
	)
	if err != nil {
		return Snapshot{}, datasource.SanitizeProbeError(err)
	}

	if err := collectCheckpointStats(queryCtx, tx, &snapshot); err != nil {
		return Snapshot{}, datasource.SanitizeProbeError(err)
	}
	now := time.Now().UTC()
	snapshot.CollectedAt = now
	snapshot.LastCollectedAt = now
	return snapshot, nil
}

func CollectStatus(ctx context.Context, config datasource.TestConfig) (model.InstanceStatus, error) {
	conn, timeout, err := datasource.Open(ctx, config)
	if err != nil {
		return model.InstanceStatus{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var result model.InstanceStatus
	if err := conn.QueryRow(queryCtx, `SELECT pg_catalog.pg_postmaster_start_time(), pg_catalog.pg_is_in_recovery(), current_setting('max_connections')::bigint`).Scan(&result.PostmasterStart, &result.InRecovery, &result.MaxConnections); err != nil {
		return model.InstanceStatus{}, datasource.SanitizeProbeError(err)
	}
	result.ObservedAt = time.Now().UTC()
	result.Connected = true
	return result, nil
}

func collectCheckpointStats(ctx context.Context, tx pgx.Tx, snapshot *Snapshot) error {
	var hasCheckpointer bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('pg_catalog.pg_stat_checkpointer') IS NOT NULL`).Scan(&hasCheckpointer); err != nil {
		return err
	}
	query := `SELECT checkpoints_timed, checkpoints_req FROM pg_catalog.pg_stat_bgwriter`
	if hasCheckpointer {
		query = `SELECT num_timed, num_requested FROM pg_catalog.pg_stat_checkpointer`
	}
	return tx.QueryRow(ctx, query).Scan(&snapshot.CheckpointsTimed, &snapshot.CheckpointsRequested)
}

// snapshotSQL 汇总一个采样点的全部指标。
// 会话类指标不再排除采集自身的连接（原先用 pid <> pg_backend_pid() 过滤）：
// 监控连接确实占用了一个连接槽位，按实际占用统计才能让使用率反映真实容量，
// 否则每台实例的使用率都会恒定少算一个连接。
const snapshotSQL = `
WITH activity AS (
  SELECT
    count(*)::bigint AS sessions_total,
    count(*) FILTER (WHERE state = 'active')::bigint AS sessions_active,
    count(*) FILTER (WHERE state LIKE 'idle in transaction%')::bigint AS sessions_idle_transaction,
    count(*) FILTER (
      WHERE state = 'active' AND wait_event IS NOT NULL
    )::bigint AS sessions_waiting,
    count(*) FILTER (
      WHERE state = 'active'
        AND query_start <= clock_timestamp() - interval '60 seconds'
    )::bigint AS long_queries,
    count(*) FILTER (
      WHERE xact_start IS NOT NULL AND state <> 'idle'
        AND xact_start <= clock_timestamp() - interval '300 seconds'
    )::bigint AS long_transactions
  FROM pg_catalog.pg_stat_activity
  WHERE backend_type = 'client backend'
), database_stats AS (
  SELECT
    coalesce(sum(xact_commit), 0)::bigint AS commits,
    coalesce(sum(xact_rollback), 0)::bigint AS rollbacks,
    coalesce(sum(blks_hit), 0)::bigint AS blocks_hit,
    coalesce(sum(blks_read), 0)::bigint AS blocks_read,
    coalesce(sum(tup_inserted), 0)::bigint AS rows_inserted,
    coalesce(sum(tup_updated), 0)::bigint AS rows_updated,
    coalesce(sum(tup_deleted), 0)::bigint AS rows_deleted,
    coalesce(sum(temp_files), 0)::bigint AS temp_files,
    coalesce(sum(temp_bytes), 0)::bigint AS temp_bytes,
    coalesce(sum(deadlocks), 0)::bigint AS deadlocks
  FROM pg_catalog.pg_stat_database s
  JOIN pg_catalog.pg_database d ON d.oid = s.datid
  WHERE NOT d.datistemplate
), lock_stats AS (
  SELECT
    count(*) FILTER (WHERE NOT locks.granted)::bigint AS locks_not_granted,
    count(DISTINCT locks.pid) FILTER (
      WHERE NOT locks.granted AND activity.backend_type = 'client backend'
    )::bigint AS blocked_sessions
  FROM pg_catalog.pg_locks locks
  LEFT JOIN pg_catalog.pg_stat_activity activity ON activity.pid = locks.pid
), replication_stats AS (
  SELECT
    count(*)::bigint AS replication_connections,
    coalesce(max(greatest(pg_catalog.pg_wal_lsn_diff(
      CASE WHEN pg_catalog.pg_is_in_recovery()
        THEN coalesce(pg_catalog.pg_last_wal_receive_lsn(), pg_catalog.pg_last_wal_replay_lsn())
        ELSE pg_catalog.pg_current_wal_lsn()
      END,
      replay_lsn
    ), 0)) FILTER (WHERE replay_lsn IS NOT NULL), 0)::bigint AS max_replay_lag_bytes,
    coalesce(bool_or(replay_lsn IS NOT NULL), false) AS max_replay_lag_bytes_available,
    coalesce(max(extract(epoch FROM replay_lag)), 0)::double precision AS max_replay_lag_seconds,
    coalesce(bool_or(replay_lag IS NOT NULL), false) AS max_replay_lag_seconds_available
  FROM pg_catalog.pg_stat_replication
)
SELECT
  a.sessions_total, a.sessions_active, a.sessions_idle_transaction,
  a.sessions_waiting, a.long_queries, a.long_transactions,
  d.commits, d.rollbacks, d.blocks_hit, d.blocks_read,
  d.rows_inserted, d.rows_updated, d.rows_deleted,
  d.temp_files, d.temp_bytes, d.deadlocks,
  l.locks_not_granted, l.blocked_sessions,
  coalesce((SELECT wal_bytes FROM pg_catalog.pg_stat_wal), 0)::bigint,
  r.replication_connections, r.max_replay_lag_bytes, r.max_replay_lag_bytes_available,
  r.max_replay_lag_seconds, r.max_replay_lag_seconds_available
FROM activity a CROSS JOIN database_stats d CROSS JOIN lock_stats l CROSS JOIN replication_stats r
`
