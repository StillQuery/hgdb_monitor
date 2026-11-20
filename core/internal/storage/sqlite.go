package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"hgdb_monitor/core/internal/model"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 6

var instanceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type SQLite struct {
	db *sql.DB
}

func Open(ctx context.Context, dataDir string) (*SQLite, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	databasePath := filepath.Join(dataDir, "monitor.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &SQLite{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(databasePath, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("protect sqlite database: %w", err)
	}
	return store, nil
}

func (s *SQLite) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS schema_meta (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL
		)`,
		`INSERT INTO schema_meta(id, version) VALUES (1, 1)
		 ON CONFLICT(id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS metric_samples (
			instance_id TEXT NOT NULL,
			metric_name TEXT NOT NULL,
			collected_at_ms INTEGER NOT NULL,
			value REAL NOT NULL,
			PRIMARY KEY(instance_id, metric_name, collected_at_ms)
		) WITHOUT ROWID`,
		`CREATE INDEX IF NOT EXISTS metric_samples_time
		 ON metric_samples(collected_at_ms)`,
		`CREATE TABLE IF NOT EXISTS metric_snapshots (
			instance_id TEXT NOT NULL,
			collected_at_ms INTEGER NOT NULL,
			sessions_total INTEGER NOT NULL,
			sessions_active INTEGER NOT NULL,
			sessions_idle_transaction INTEGER NOT NULL,
			sessions_waiting INTEGER NOT NULL,
			long_queries INTEGER NOT NULL,
			long_transactions INTEGER NOT NULL,
			commits INTEGER NOT NULL,
			rollbacks INTEGER NOT NULL,
			blocks_hit INTEGER NOT NULL,
			blocks_read INTEGER NOT NULL,
			rows_inserted INTEGER NOT NULL,
			rows_updated INTEGER NOT NULL,
			rows_deleted INTEGER NOT NULL,
			temp_files INTEGER NOT NULL,
			temp_bytes INTEGER NOT NULL,
			deadlocks INTEGER NOT NULL,
			locks_not_granted INTEGER NOT NULL,
			blocked_sessions INTEGER NOT NULL,
			wal_bytes INTEGER NOT NULL,
			checkpoints_timed INTEGER NOT NULL,
			checkpoints_requested INTEGER NOT NULL,
			replication_connections INTEGER NOT NULL,
			max_replay_lag_bytes INTEGER NOT NULL,
			max_replay_lag_bytes_available INTEGER NOT NULL DEFAULT 0,
			max_replay_lag_seconds REAL NOT NULL,
			max_replay_lag_seconds_available INTEGER NOT NULL DEFAULT 0,
			connected INTEGER NOT NULL,
			postmaster_start_ms INTEGER NOT NULL,
			in_recovery INTEGER NOT NULL,
			max_connections INTEGER NOT NULL,
			last_collected_at_ms INTEGER NOT NULL,
			PRIMARY KEY(instance_id, collected_at_ms)
		) WITHOUT ROWID`,
		`CREATE INDEX IF NOT EXISTS metric_snapshots_time
		 ON metric_snapshots(collected_at_ms)`,
		`CREATE TABLE IF NOT EXISTS datasources (
			id TEXT PRIMARY KEY,
			instance_ref TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			host TEXT NOT NULL,
			port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
			database_name TEXT NOT NULL,
			user_name TEXT NOT NULL,
			ssl_mode TEXT NOT NULL,
			persistent_enabled INTEGER NOT NULL DEFAULT 0,
			verified_at_ms INTEGER,
			created_at_ms INTEGER NOT NULL,
			updated_at_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS datasources_persistent
		 ON datasources(persistent_enabled)`,
		`CREATE TABLE IF NOT EXISTS instance_status (
			instance_id TEXT PRIMARY KEY,
			observed_at_ms INTEGER NOT NULL,
			connected INTEGER NOT NULL,
			postmaster_start_ms INTEGER NOT NULL,
			in_recovery INTEGER NOT NULL,
			max_connections INTEGER NOT NULL
		) WITHOUT ROWID`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize sqlite schema: %w", err)
		}
	}
	for name, definition := range map[string]string{
		"connect_timeout_seconds": "INTEGER NOT NULL DEFAULT 5",
		"query_timeout_seconds":   "INTEGER NOT NULL DEFAULT 10",
		"capabilities_json":       "TEXT NOT NULL DEFAULT '{}'",
	} {
		if err := s.ensureDatasourceColumn(ctx, name, definition); err != nil {
			return err
		}
	}
	if err := s.ensureTableColumn(ctx, "metric_snapshots", "max_replay_lag_seconds_available", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "metric_snapshots", "max_replay_lag_bytes_available", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_meta SET version = 6 WHERE id = 1 AND version < 6`); err != nil {
		return fmt.Errorf("update sqlite schema version: %w", err)
	}
	return nil
}

func (s *SQLite) ensureDatasourceColumn(ctx context.Context, name, definition string) error {
	return s.ensureTableColumn(ctx, "datasources", name, definition)
}

func (s *SQLite) ensureTableColumn(ctx context.Context, table, name, definition string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("inspect datasource schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid int
		var column, kind string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &column, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if column == name {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+name+` `+definition); err != nil {
		return fmt.Errorf("add %s column %s: %w", table, name, err)
	}
	return nil
}

func (s *SQLite) DatasourceCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM datasources`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count datasources: %w", err)
	}
	return count, nil
}

func (s *SQLite) CreateDatasource(ctx context.Context, source model.Datasource, maximum int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin datasource creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM datasources`).Scan(&count); err != nil {
		return fmt.Errorf("count datasources: %w", err)
	}
	if count >= maximum {
		return ErrDatasourceLimit
	}
	capabilities, err := json.Marshal(source.Capabilities)
	if err != nil {
		return fmt.Errorf("encode datasource capabilities: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO datasources (
			id, instance_ref, display_name, host, port, database_name, user_name,
			ssl_mode, persistent_enabled, verified_at_ms, created_at_ms, updated_at_ms,
			connect_timeout_seconds, query_timeout_seconds, capabilities_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, source.ID, source.InstanceRef, source.DisplayName, source.Host, source.Port,
		source.Database, source.User, source.SSLMode, source.PersistentEnabled,
		nullTimeMillis(source.VerifiedAt), source.CreatedAt.UnixMilli(), source.UpdatedAt.UnixMilli(), source.ConnectTimeout, source.QueryTimeout, string(capabilities))
	if err != nil {
		return fmt.Errorf("create datasource: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit datasource creation: %w", err)
	}
	return nil
}

var ErrDatasourceLimit = fmt.Errorf("datasource limit reached")
var ErrDatasourceNotFound = fmt.Errorf("datasource not found")
var ErrSnapshotNotFound = fmt.Errorf("metric snapshot not found")
var ErrStatusNotFound = fmt.Errorf("instance status not found")

func (s *SQLite) ListDatasources(ctx context.Context) ([]model.Datasource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, instance_ref, display_name, host, port, database_name, user_name,
		       ssl_mode, persistent_enabled, verified_at_ms, created_at_ms, updated_at_ms,
		       connect_timeout_seconds, query_timeout_seconds, capabilities_json
		FROM datasources ORDER BY created_at_ms, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}
	defer rows.Close()
	result := make([]model.Datasource, 0)
	for rows.Next() {
		source, err := scanDatasource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}
	return result, nil
}

func (s *SQLite) GetDatasource(ctx context.Context, id string) (model.Datasource, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, instance_ref, display_name, host, port, database_name, user_name,
		       ssl_mode, persistent_enabled, verified_at_ms, created_at_ms, updated_at_ms,
		       connect_timeout_seconds, query_timeout_seconds, capabilities_json
		FROM datasources WHERE id = ?
	`, id)
	source, err := scanDatasource(row)
	if err == sql.ErrNoRows {
		return model.Datasource{}, ErrDatasourceNotFound
	}
	return source, err
}

type rowScanner interface{ Scan(...any) error }

func scanDatasource(row rowScanner) (model.Datasource, error) {
	var source model.Datasource
	var persistent bool
	var verified sql.NullInt64
	var created, updated int64
	var capabilities string
	if err := row.Scan(&source.ID, &source.InstanceRef, &source.DisplayName, &source.Host,
		&source.Port, &source.Database, &source.User, &source.SSLMode, &persistent,
		&verified, &created, &updated, &source.ConnectTimeout, &source.QueryTimeout, &capabilities); err != nil {
		return model.Datasource{}, err
	}
	source.PersistentEnabled = persistent
	if verified.Valid {
		value := time.UnixMilli(verified.Int64).UTC()
		source.VerifiedAt = &value
	}
	source.CreatedAt = time.UnixMilli(created).UTC()
	source.UpdatedAt = time.UnixMilli(updated).UTC()
	if err := json.Unmarshal([]byte(capabilities), &source.Capabilities); err != nil {
		return model.Datasource{}, fmt.Errorf("decode datasource capabilities: %w", err)
	}
	return source, nil
}

func (s *SQLite) UpdateDatasource(ctx context.Context, source model.Datasource) error {
	capabilities, err := json.Marshal(source.Capabilities)
	if err != nil {
		return fmt.Errorf("encode datasource capabilities: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE datasources SET display_name = ?, host = ?, port = ?, database_name = ?,
		       user_name = ?, ssl_mode = ?, persistent_enabled = ?, verified_at_ms = ?,
		       updated_at_ms = ?, connect_timeout_seconds = ?, query_timeout_seconds = ?, capabilities_json = ? WHERE id = ?
	`, source.DisplayName, source.Host, source.Port, source.Database, source.User,
		source.SSLMode, source.PersistentEnabled, nullTimeMillis(source.VerifiedAt),
		source.UpdatedAt.UnixMilli(), source.ConnectTimeout, source.QueryTimeout, string(capabilities), source.ID)
	if err != nil {
		return fmt.Errorf("update datasource: %w", err)
	}
	return requireChanged(result)
}

func (s *SQLite) DeleteDatasource(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin datasource deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM metric_snapshots WHERE instance_id = ?`, id); err != nil {
		return fmt.Errorf("delete datasource snapshots: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM instance_status WHERE instance_id = ?`, id); err != nil {
		return fmt.Errorf("delete datasource status: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM datasources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete datasource: %w", err)
	}
	if err := requireChanged(result); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) SaveInstanceStatus(ctx context.Context, instanceID string, status model.InstanceStatus) error {
	if !instanceIDPattern.MatchString(instanceID) {
		return fmt.Errorf("invalid instance id")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO instance_status(instance_id, observed_at_ms, connected, postmaster_start_ms, in_recovery, max_connections)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(instance_id) DO UPDATE SET observed_at_ms=excluded.observed_at_ms, connected=excluded.connected,
		postmaster_start_ms=excluded.postmaster_start_ms, in_recovery=excluded.in_recovery, max_connections=excluded.max_connections`,
		instanceID, status.ObservedAt.UnixMilli(), status.Connected, status.PostmasterStart.UnixMilli(), status.InRecovery, status.MaxConnections)
	if err != nil {
		return fmt.Errorf("save instance status: %w", err)
	}
	return nil
}

func (s *SQLite) LatestInstanceStatus(ctx context.Context, instanceID string) (model.InstanceStatus, error) {
	if !instanceIDPattern.MatchString(instanceID) {
		return model.InstanceStatus{}, fmt.Errorf("invalid instance id")
	}
	var result model.InstanceStatus
	var observed, started int64
	err := s.db.QueryRowContext(ctx, `SELECT observed_at_ms, connected, postmaster_start_ms, in_recovery, max_connections FROM instance_status WHERE instance_id = ?`, instanceID).
		Scan(&observed, &result.Connected, &started, &result.InRecovery, &result.MaxConnections)
	if err == sql.ErrNoRows {
		return model.InstanceStatus{}, ErrStatusNotFound
	}
	if err != nil {
		return model.InstanceStatus{}, fmt.Errorf("read instance status: %w", err)
	}
	result.ObservedAt = time.UnixMilli(observed).UTC()
	result.PostmasterStart = time.UnixMilli(started).UTC()
	return result, nil
}

func (s *SQLite) DeleteSnapshotsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for {
		result, err := s.db.ExecContext(ctx, `DELETE FROM metric_snapshots
			WHERE (instance_id, collected_at_ms) IN (
				SELECT instance_id, collected_at_ms FROM metric_snapshots
				WHERE collected_at_ms < ? ORDER BY collected_at_ms LIMIT 500
			)`, cutoff.UnixMilli())
		if err != nil {
			return total, fmt.Errorf("delete expired snapshots: %w", err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("count expired snapshots: %w", err)
		}
		total += deleted
		if deleted < 500 {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}

const snapshotColumns = `
	collected_at_ms,
	sessions_total, sessions_active, sessions_idle_transaction, sessions_waiting,
	long_queries, long_transactions,
	commits, rollbacks, blocks_hit, blocks_read,
	rows_inserted, rows_updated, rows_deleted,
	temp_files, temp_bytes, deadlocks,
	locks_not_granted, blocked_sessions,
	wal_bytes, checkpoints_timed, checkpoints_requested,
	replication_connections, max_replay_lag_bytes, max_replay_lag_bytes_available, max_replay_lag_seconds, max_replay_lag_seconds_available,
	connected, postmaster_start_ms, in_recovery, max_connections, last_collected_at_ms`

func (s *SQLite) LatestSnapshot(ctx context.Context, instanceID string) (model.Snapshot, error) {
	if !instanceIDPattern.MatchString(instanceID) {
		return model.Snapshot{}, fmt.Errorf("invalid instance id")
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+snapshotColumns+`
		FROM metric_snapshots WHERE instance_id = ? ORDER BY collected_at_ms DESC LIMIT 1`, instanceID)
	snapshot, err := scanSnapshot(row)
	if err == sql.ErrNoRows {
		return model.Snapshot{}, ErrSnapshotNotFound
	}
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("read latest snapshot: %w", err)
	}
	return snapshot, nil
}

func (s *SQLite) QuerySnapshots(ctx context.Context, instanceID string, from, to time.Time) ([]model.Snapshot, error) {
	if !instanceIDPattern.MatchString(instanceID) {
		return nil, fmt.Errorf("invalid instance id")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+snapshotColumns+`
		FROM metric_snapshots
		WHERE instance_id = ? AND collected_at_ms >= ? AND collected_at_ms <= ?
		ORDER BY collected_at_ms`, instanceID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("query metric snapshots: %w", err)
	}
	defer rows.Close()
	result := make([]model.Snapshot, 0)
	for rows.Next() {
		snapshot, scanErr := scanSnapshot(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan metric snapshot: %w", scanErr)
		}
		result = append(result, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query metric snapshots: %w", err)
	}
	return result, nil
}

func scanSnapshot(row rowScanner) (model.Snapshot, error) {
	var snapshot model.Snapshot
	var collectedAt, postmasterStart, lastCollectedAt int64
	err := row.Scan(
		&collectedAt,
		&snapshot.SessionsTotal, &snapshot.SessionsActive, &snapshot.SessionsIdleTransaction, &snapshot.SessionsWaiting,
		&snapshot.LongQueries, &snapshot.LongTransactions,
		&snapshot.Commits, &snapshot.Rollbacks, &snapshot.BlocksHit, &snapshot.BlocksRead,
		&snapshot.RowsInserted, &snapshot.RowsUpdated, &snapshot.RowsDeleted,
		&snapshot.TempFiles, &snapshot.TempBytes, &snapshot.Deadlocks,
		&snapshot.LocksNotGranted, &snapshot.BlockedSessions,
		&snapshot.WALBytes, &snapshot.CheckpointsTimed, &snapshot.CheckpointsRequested,
		&snapshot.ReplicationConnections, &snapshot.MaxReplayLagBytes, &snapshot.MaxReplayLagBytesAvailable, &snapshot.MaxReplayLagSeconds, &snapshot.MaxReplayLagSecondsAvailable,
		&snapshot.Connected, &postmasterStart, &snapshot.InRecovery, &snapshot.MaxConnections, &lastCollectedAt,
	)
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot.CollectedAt = time.UnixMilli(collectedAt).UTC()
	snapshot.PostmasterStart = time.UnixMilli(postmasterStart).UTC()
	snapshot.LastCollectedAt = time.UnixMilli(lastCollectedAt).UTC()
	return snapshot, nil
}

func nullTimeMillis(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UnixMilli()
}

func requireChanged(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if count == 0 {
		return ErrDatasourceNotFound
	}
	return nil
}

func (s *SQLite) Ready(ctx context.Context) bool {
	return s.db.PingContext(ctx) == nil
}

func (s *SQLite) SaveSnapshot(ctx context.Context, instanceID string, snapshot model.Snapshot) error {
	if !instanceIDPattern.MatchString(instanceID) {
		return fmt.Errorf("invalid instance id")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO metric_snapshots (
			instance_id, collected_at_ms,
			sessions_total, sessions_active, sessions_idle_transaction, sessions_waiting,
			long_queries, long_transactions,
			commits, rollbacks, blocks_hit, blocks_read,
			rows_inserted, rows_updated, rows_deleted,
			temp_files, temp_bytes, deadlocks,
			locks_not_granted, blocked_sessions,
			wal_bytes, checkpoints_timed, checkpoints_requested,
			replication_connections, max_replay_lag_bytes, max_replay_lag_bytes_available, max_replay_lag_seconds, max_replay_lag_seconds_available,
			connected, postmaster_start_ms, in_recovery, max_connections, last_collected_at_ms
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)
	`,
		instanceID, snapshot.CollectedAt.UnixMilli(),
		snapshot.SessionsTotal, snapshot.SessionsActive, snapshot.SessionsIdleTransaction, snapshot.SessionsWaiting,
		snapshot.LongQueries, snapshot.LongTransactions,
		snapshot.Commits, snapshot.Rollbacks, snapshot.BlocksHit, snapshot.BlocksRead,
		snapshot.RowsInserted, snapshot.RowsUpdated, snapshot.RowsDeleted,
		snapshot.TempFiles, snapshot.TempBytes, snapshot.Deadlocks,
		snapshot.LocksNotGranted, snapshot.BlockedSessions,
		snapshot.WALBytes, snapshot.CheckpointsTimed, snapshot.CheckpointsRequested,
		snapshot.ReplicationConnections, snapshot.MaxReplayLagBytes, snapshot.MaxReplayLagBytesAvailable, snapshot.MaxReplayLagSeconds, snapshot.MaxReplayLagSecondsAvailable,
		snapshot.Connected, snapshot.PostmasterStart.UnixMilli(), snapshot.InRecovery, snapshot.MaxConnections,
		snapshot.LastCollectedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save metric snapshot: %w", err)
	}
	return nil
}

func (s *SQLite) SnapshotCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM metric_snapshots`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count metric snapshots: %w", err)
	}
	return count, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}
