package inspector

import (
	"context"

	"github.com/jackc/pgx/v5"

	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/model"
)

type ReplicationConnection struct {
	PID              int32   `json:"pid"`
	ApplicationName  string  `json:"applicationName"`
	ClientAddress    string  `json:"clientAddress,omitempty"`
	State            string  `json:"state"`
	SyncState        string  `json:"syncState"`
	SentLSN          string  `json:"sentLsn,omitempty"`
	WriteLSN         string  `json:"writeLsn,omitempty"`
	FlushLSN         string  `json:"flushLsn,omitempty"`
	ReplayLSN        string  `json:"replayLsn,omitempty"`
	ReplayLagBytes   int64   `json:"replayLagBytes"`
	ReplayLagSeconds float64 `json:"replayLagSeconds"`
}

type ReplicationSlot struct {
	Name          string `json:"name"`
	SlotType      string `json:"slotType"`
	Plugin        string `json:"plugin,omitempty"`
	Database      string `json:"database,omitempty"`
	Active        bool   `json:"active"`
	RetainedBytes int64  `json:"retainedBytes"`
}

type ReplicationResult struct {
	Role             string                  `json:"role"`
	Connections      []ReplicationConnection `json:"connections"`
	Slots            []ReplicationSlot       `json:"slots,omitempty"`
	ReceiveLSN       string                  `json:"receiveLsn,omitempty"`
	ReplayLSN        string                  `json:"replayLsn,omitempty"`
	ReplayLagBytes   int64                   `json:"replayLagBytes"`
	ReplayLagSeconds float64                 `json:"replayLagSeconds"`
}

func Replication(ctx context.Context, config datasource.TestConfig, includeSlots bool) (ReplicationResult, error) {
	conn, timeout, err := datasource.Open(ctx, config)
	if err != nil {
		return ReplicationResult{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tx, err := conn.BeginTx(queryCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ReplicationResult{}, &datasource.PublicError{Code: "READ_ONLY_SETUP_FAILED", Message: "could not start a read-only database transaction"}
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var standby bool
	if err := tx.QueryRow(queryCtx, `SELECT pg_catalog.pg_is_in_recovery()`).Scan(&standby); err != nil {
		return ReplicationResult{}, datasource.SanitizeProbeError(err)
	}
	result := ReplicationResult{Role: model.RoleFromRecovery(standby), Connections: []ReplicationConnection{}}
	if standby {
		var receive, replay *string
		var lagSeconds *float64
		if err := tx.QueryRow(queryCtx, `SELECT pg_catalog.pg_last_wal_receive_lsn()::text, pg_catalog.pg_last_wal_replay_lsn()::text,
			CASE WHEN pg_catalog.pg_last_xact_replay_timestamp() IS NULL THEN NULL ELSE extract(epoch FROM clock_timestamp()-pg_catalog.pg_last_xact_replay_timestamp()) END`).Scan(&receive, &replay, &lagSeconds); err != nil {
			return ReplicationResult{}, datasource.SanitizeProbeError(err)
		}
		if receive != nil {
			result.ReceiveLSN = *receive
		}
		if replay != nil {
			result.ReplayLSN = *replay
		}
		if receive != nil && replay != nil {
			_ = tx.QueryRow(queryCtx, `SELECT coalesce(pg_catalog.pg_wal_lsn_diff($1::pg_lsn,$2::pg_lsn),0)::bigint`, *receive, *replay).Scan(&result.ReplayLagBytes)
		}
		if lagSeconds != nil {
			result.ReplayLagSeconds = *lagSeconds
		}
	} else {
		rows, queryErr := tx.Query(queryCtx, `SELECT pid, coalesce(application_name,''), coalesce(client_addr::text,''), coalesce(state,''), coalesce(sync_state,''),
			sent_lsn::text, write_lsn::text, flush_lsn::text, replay_lsn::text,
			coalesce(pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), replay_lsn),0)::bigint,
			coalesce(extract(epoch FROM replay_lag),0)::double precision FROM pg_catalog.pg_stat_replication ORDER BY pid`)
		if queryErr != nil {
			return ReplicationResult{}, datasource.SanitizeProbeError(queryErr)
		}
		for rows.Next() {
			var item ReplicationConnection
			var sent, write, flush, replay *string
			if err := rows.Scan(&item.PID, &item.ApplicationName, &item.ClientAddress, &item.State, &item.SyncState, &sent, &write, &flush, &replay, &item.ReplayLagBytes, &item.ReplayLagSeconds); err != nil {
				rows.Close()
				return ReplicationResult{}, datasource.SanitizeProbeError(err)
			}
			if sent != nil {
				item.SentLSN = *sent
			}
			if write != nil {
				item.WriteLSN = *write
			}
			if flush != nil {
				item.FlushLSN = *flush
			}
			if replay != nil {
				item.ReplayLSN = *replay
			}
			result.Connections = append(result.Connections, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ReplicationResult{}, datasource.SanitizeProbeError(err)
		}
		rows.Close()
	}
	if includeSlots {
		currentLSN := "pg_catalog.pg_current_wal_lsn()"
		if standby {
			currentLSN = "pg_catalog.pg_last_wal_receive_lsn()"
		}
		rows, queryErr := tx.Query(queryCtx, `SELECT slot_name, slot_type, coalesce(plugin,''), coalesce(database::text,''), active,
			coalesce(pg_catalog.pg_wal_lsn_diff(`+currentLSN+`, restart_lsn),0)::bigint FROM pg_catalog.pg_replication_slots ORDER BY slot_name`)
		if queryErr != nil {
			return ReplicationResult{}, datasource.SanitizeProbeError(queryErr)
		}
		for rows.Next() {
			var item ReplicationSlot
			if err := rows.Scan(&item.Name, &item.SlotType, &item.Plugin, &item.Database, &item.Active, &item.RetainedBytes); err != nil {
				rows.Close()
				return ReplicationResult{}, datasource.SanitizeProbeError(err)
			}
			result.Slots = append(result.Slots, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ReplicationResult{}, datasource.SanitizeProbeError(err)
		}
		rows.Close()
	}
	return result, nil
}
