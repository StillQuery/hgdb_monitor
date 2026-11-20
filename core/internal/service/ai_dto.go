package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/inspector"
	"hgdb_monitor/core/internal/model"
	"hgdb_monitor/core/internal/privacy"
	"hgdb_monitor/core/internal/storage"
)

type AICommon struct {
	InstanceRef      string    `json:"instanceRef"`
	ObservedAt       time.Time `json:"observedAt"`
	Source           string    `json:"source"`
	FreshnessSeconds float64   `json:"freshnessSeconds"`
	Truncated        bool      `json:"truncated"`
	Warnings         []string  `json:"warnings"`
}

type AIOverviewResult struct {
	AICommon
	Overview model.Snapshot  `json:"overview"`
	Deltas   *OverviewDeltas `json:"deltas,omitempty"`
}

type OverviewDeltas struct {
	IntervalSeconds       float64 `json:"intervalSeconds"`
	TransactionsPerSecond float64 `json:"transactionsPerSecond"`
	CommitsPerSecond      float64 `json:"commitsPerSecond"`
	RollbacksPerSecond    float64 `json:"rollbacksPerSecond"`
	CacheHitRatio         float64 `json:"cacheHitRatio"`
	RowsChangedPerSecond  float64 `json:"rowsChangedPerSecond"`
	TempBytesPerSecond    float64 `json:"tempBytesPerSecond"`
	DeadlocksPerSecond    float64 `json:"deadlocksPerSecond"`
	WALBytesPerSecond     float64 `json:"walBytesPerSecond"`
	CheckpointsPerSecond  float64 `json:"checkpointsPerSecond"`
}

type AIActivityItem struct {
	SessionRef            string  `json:"sessionRef"`
	DatabaseRef           string  `json:"databaseRef,omitempty"`
	UserRef               string  `json:"userRef,omitempty"`
	ApplicationRef        string  `json:"applicationRef,omitempty"`
	State                 string  `json:"state"`
	WaitEventType         string  `json:"waitEventType,omitempty"`
	WaitEvent             string  `json:"waitEvent,omitempty"`
	QueryAgeSeconds       float64 `json:"queryAgeSeconds"`
	TransactionAgeSeconds float64 `json:"transactionAgeSeconds"`
	SQL                   string  `json:"sql"`
}

type AIActivitiesResult struct {
	AICommon
	Items []AIActivityItem `json:"items"`
}

type AILockWaitItem struct {
	BlockedSessionRef      string  `json:"blockedSessionRef"`
	BlockingSessionRef     string  `json:"blockingSessionRef,omitempty"`
	BlockingTransactionRef string  `json:"blockingTransactionRef,omitempty"`
	BlockerType            string  `json:"blockerType"`
	BlockedDatabaseRef     string  `json:"blockedDatabaseRef,omitempty"`
	BlockedUserRef         string  `json:"blockedUserRef,omitempty"`
	BlockingDatabaseRef    string  `json:"blockingDatabaseRef,omitempty"`
	BlockingUserRef        string  `json:"blockingUserRef,omitempty"`
	LockType               string  `json:"lockType,omitempty"`
	RequestedMode          string  `json:"requestedMode,omitempty"`
	SchemaName             string  `json:"schemaName,omitempty"`
	RelationName           string  `json:"relationName,omitempty"`
	QueryAgeSeconds        float64 `json:"queryAgeSeconds"`
	BlockedSQL             string  `json:"blockedSql"`
	BlockingSQL            string  `json:"blockingSql"`
}

type AILockWaitsResult struct {
	AICommon
	Items []AILockWaitItem `json:"items"`
}

type AITopSQLItem struct {
	StatementID       string  `json:"statementId"`
	DatabaseRef       string  `json:"databaseRef"`
	UserRef           string  `json:"userRef"`
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

type AITopSQLResult struct {
	AICommon
	Items      []AITopSQLItem `json:"items"`
	StatsReset *time.Time     `json:"statsReset,omitempty"`
}

type AIReplicationConnection struct {
	StandbyRef              string   `json:"standbyRef"`
	State                   string   `json:"state"`
	SyncState               string   `json:"syncState"`
	SentLSN                 string   `json:"sentLsn,omitempty"`
	WriteLSN                string   `json:"writeLsn,omitempty"`
	FlushLSN                string   `json:"flushLsn,omitempty"`
	ReplayLSN               string   `json:"replayLsn,omitempty"`
	ReplayLagBytes          int64    `json:"replayLagBytes"`
	ReplayLagBytesAvailable bool     `json:"replayLagBytesAvailable"`
	ReplayLagSeconds        *float64 `json:"replayLagSeconds,omitempty"`
}

type AIReplicationSlot struct {
	SlotRef       string `json:"slotRef"`
	SlotType      string `json:"slotType"`
	Active        bool   `json:"active"`
	RetainedBytes int64  `json:"retainedBytes"`
}
type AIReplicationResult struct {
	AICommon
	Role                    string                    `json:"role"`
	Connections             []AIReplicationConnection `json:"connections"`
	Slots                   []AIReplicationSlot       `json:"slots,omitempty"`
	ReceiveLSN              string                    `json:"receiveLsn,omitempty"`
	ReplayLSN               string                    `json:"replayLsn,omitempty"`
	ReplayLagBytes          int64                     `json:"replayLagBytes"`
	ReplayLagBytesAvailable bool                      `json:"replayLagBytesAvailable"`
	LastReplayAgeSeconds    *float64                  `json:"lastReplayAgeSeconds,omitempty"`
}

type AIExplainResult struct {
	AICommon
	Format         string            `json:"format"`
	Plan           any               `json:"plan"`
	PlanningTimeMS *float64          `json:"planningTimeMs,omitempty"`
	Objects        []AIExplainObject `json:"objects"`
}

type AIExplainObject struct {
	NodeType      string  `json:"nodeType"`
	SchemaName    string  `json:"schemaName,omitempty"`
	RelationName  string  `json:"relationName,omitempty"`
	IndexName     string  `json:"indexName,omitempty"`
	EstimatedRows float64 `json:"estimatedRows,omitempty"`
	TotalCost     float64 `json:"totalCost,omitempty"`
}

func common(instanceRef, source string, observed time.Time, truncated bool) AICommon {
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	return AICommon{InstanceRef: instanceRef, ObservedAt: observed, Source: source,
		FreshnessSeconds: max(0, time.Since(observed).Seconds()), Truncated: truncated, Warnings: []string{}}
}

func anonymousID(instanceRef, kind, value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(instanceRef + "\x00" + kind + "\x00" + value))
	return kind + "_" + hex.EncodeToString(sum[:6])
}

func (r *Runtime) AIOverview(ctx context.Context, instanceRef string, includeDeltas bool) (AIOverviewResult, error) {
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return AIOverviewResult{}, err
	}
	snapshot, err := r.AIImmediate(ctx, instanceRef)
	if err != nil {
		return AIOverviewResult{}, err
	}
	result := AIOverviewResult{AICommon: common(instanceRef, "live", snapshot.CollectedAt, false), Overview: snapshot}
	if includeDeltas {
		previous, previousErr := r.repo.LatestSnapshot(ctx, source.ID)
		if previousErr != nil {
			if errors.Is(previousErr, storage.ErrSnapshotNotFound) {
				result.Warnings = append(result.Warnings, "NO_DELTA_BASELINE")
			} else {
				return AIOverviewResult{}, previousErr
			}
		} else if counterBoundaryChanged(previous, snapshot) || overviewCounterDecreased(previous, snapshot) {
			result.Warnings = append(result.Warnings, "COUNTER_RESET_DETECTED")
		} else if seconds := snapshot.CollectedAt.Sub(previous.CollectedAt).Seconds(); seconds > 0 && seconds <= 120 {
			commits := counterIncrease(previous.Commits, snapshot.Commits)
			rollbacks := counterIncrease(previous.Rollbacks, snapshot.Rollbacks)
			hits := counterIncrease(previous.BlocksHit, snapshot.BlocksHit)
			reads := counterIncrease(previous.BlocksRead, snapshot.BlocksRead)
			cacheRatio := float64(0)
			if hits+reads > 0 {
				cacheRatio = float64(hits) / float64(hits+reads)
			}
			result.Deltas = &OverviewDeltas{IntervalSeconds: seconds,
				TransactionsPerSecond: float64(commits+rollbacks) / seconds, CommitsPerSecond: float64(commits) / seconds,
				RollbacksPerSecond: float64(rollbacks) / seconds, CacheHitRatio: cacheRatio,
				RowsChangedPerSecond: float64(counterIncrease(previous.RowsInserted, snapshot.RowsInserted)+counterIncrease(previous.RowsUpdated, snapshot.RowsUpdated)+counterIncrease(previous.RowsDeleted, snapshot.RowsDeleted)) / seconds,
				TempBytesPerSecond:   float64(counterIncrease(previous.TempBytes, snapshot.TempBytes)) / seconds,
				DeadlocksPerSecond:   float64(counterIncrease(previous.Deadlocks, snapshot.Deadlocks)) / seconds,
				WALBytesPerSecond:    float64(counterIncrease(previous.WALBytes, snapshot.WALBytes)) / seconds,
				CheckpointsPerSecond: float64(counterIncrease(previous.CheckpointsTimed, snapshot.CheckpointsTimed)+counterIncrease(previous.CheckpointsRequested, snapshot.CheckpointsRequested)) / seconds}
		} else {
			result.Warnings = append(result.Warnings, "STALE_DELTA_BASELINE")
		}
	}
	return result, nil
}

func counterIncrease(previous, current int64) int64 {
	if current >= previous {
		return current - previous
	}
	if current > 0 {
		return current
	}
	return 0
}

func overviewCounterDecreased(previous, current model.Snapshot) bool {
	return current.Commits < previous.Commits || current.Rollbacks < previous.Rollbacks ||
		current.BlocksHit < previous.BlocksHit || current.BlocksRead < previous.BlocksRead ||
		current.RowsInserted < previous.RowsInserted || current.RowsUpdated < previous.RowsUpdated || current.RowsDeleted < previous.RowsDeleted ||
		current.TempBytes < previous.TempBytes || current.Deadlocks < previous.Deadlocks || current.WALBytes < previous.WALBytes ||
		current.CheckpointsTimed < previous.CheckpointsTimed || current.CheckpointsRequested < previous.CheckpointsRequested
}

func (r *Runtime) AIActivitiesSafe(ctx context.Context, instanceRef string, kind inspector.ActivityKind, limit int) (AIActivitiesResult, error) {
	return r.AIActivitiesSafeWithMinimum(ctx, instanceRef, kind, 0, limit)
}

func (r *Runtime) AIActivitiesSafeWithMinimum(ctx context.Context, instanceRef string, kind inspector.ActivityKind, minimumSeconds, limit int) (AIActivitiesResult, error) {
	if limit < 1 || limit > 20 {
		return AIActivitiesResult{}, &datasource.PublicError{Code: "INVALID_LIMIT", Message: "session limit must be between 1 and 20"}
	}
	items, err := r.AIActivitiesWithMinimum(ctx, instanceRef, kind, minimumSeconds, limit)
	if err != nil {
		return AIActivitiesResult{}, err
	}
	result := AIActivitiesResult{AICommon: common(instanceRef, "live", time.Now().UTC(), false), Items: make([]AIActivityItem, 0, len(items))}
	for _, item := range items {
		result.Items = append(result.Items, AIActivityItem{SessionRef: anonymousID(instanceRef, "session", fmt.Sprint(item.PID)),
			DatabaseRef: anonymousID(instanceRef, "database", item.Database), UserRef: anonymousID(instanceRef, "user", item.User),
			ApplicationRef: anonymousID(instanceRef, "application", item.ApplicationName), State: item.State, WaitEventType: item.WaitEventType,
			WaitEvent: item.WaitEvent, QueryAgeSeconds: item.QueryAgeSeconds, TransactionAgeSeconds: item.TransactionAgeSeconds, SQL: privacy.RedactSQLLimit(item.SQL, 500)})
	}
	return result, nil
}

func (r *Runtime) AILockWaitsSafe(ctx context.Context, instanceRef string, limit int) (AILockWaitsResult, error) {
	if limit < 1 || limit > 20 {
		return AILockWaitsResult{}, &datasource.PublicError{Code: "INVALID_LIMIT", Message: "blocking-chain limit must be between 1 and 20"}
	}
	items, err := r.AILockWaits(ctx, instanceRef, limit)
	if err != nil {
		return AILockWaitsResult{}, err
	}
	result := AILockWaitsResult{AICommon: common(instanceRef, "live", time.Now().UTC(), false), Items: make([]AILockWaitItem, 0, len(items))}
	for _, item := range items {
		blockingSessionRef := ""
		blockingTransactionRef := ""
		if item.BlockerType == "prepared_transaction" {
			blockingTransactionRef = anonymousID(instanceRef, "prepared_transaction", item.BlockerIdentity)
		} else {
			blockingSessionRef = anonymousID(instanceRef, "session", fmt.Sprint(item.BlockingPID))
		}
		result.Items = append(result.Items, AILockWaitItem{
			BlockedSessionRef: anonymousID(instanceRef, "session", fmt.Sprint(item.BlockedPID)), BlockingSessionRef: blockingSessionRef,
			BlockingTransactionRef: blockingTransactionRef, BlockerType: item.BlockerType,
			BlockedDatabaseRef: anonymousID(instanceRef, "database", item.BlockedDatabase), BlockedUserRef: anonymousID(instanceRef, "user", item.BlockedUser),
			BlockingDatabaseRef: anonymousID(instanceRef, "database", item.BlockingDatabase), BlockingUserRef: anonymousID(instanceRef, "user", item.BlockingUser),
			LockType: item.LockType, RequestedMode: item.RequestedMode, SchemaName: item.SchemaName, RelationName: item.RelationName, QueryAgeSeconds: item.QueryAgeSeconds,
			BlockedSQL: privacy.RedactSQLLimit(item.BlockedSQL, 500), BlockingSQL: privacy.RedactSQLLimit(item.BlockingSQL, 500)})
	}
	return result, nil
}

func (r *Runtime) TopSQL(ctx context.Context, id string, order inspector.TopSQLOrder, limit int) (inspector.TopSQLResult, error) {
	release, err := r.acquire(ctx, id)
	if err != nil {
		return inspector.TopSQLResult{}, err
	}
	defer release()
	_, cfg, err := r.connectionConfig(ctx, id)
	if err != nil {
		return inspector.TopSQLResult{}, err
	}
	return inspector.TopSQL(ctx, cfg, order, limit)
}
func (r *Runtime) AITopSQL(ctx context.Context, ref string, order inspector.TopSQLOrder, limit int) (AITopSQLResult, error) {
	source, err := r.resolveInstanceRef(ctx, ref)
	if err != nil {
		return AITopSQLResult{}, err
	}
	release, err := r.acquire(ctx, source.ID)
	if err != nil {
		return AITopSQLResult{}, err
	}
	defer release()
	_, cfg, err := r.connectionConfig(ctx, source.ID)
	if err != nil {
		return AITopSQLResult{}, err
	}
	raw, err := inspector.TopSQL(ctx, cfg, order, limit)
	if err != nil {
		return AITopSQLResult{}, err
	}
	result := AITopSQLResult{AICommon: common(ref, "live", time.Now().UTC(), false), Items: make([]AITopSQLItem, 0, len(raw.Items)), StatsReset: raw.StatsReset}
	for _, item := range raw.Items {
		result.Items = append(result.Items, AITopSQLItem{StatementID: anonymousID(ref, "statement", item.QueryID), DatabaseRef: anonymousID(ref, "database", fmt.Sprint(item.DatabaseOID)), UserRef: anonymousID(ref, "user", fmt.Sprint(item.UserOID)), SQL: privacy.RedactSQLLimit(item.SQL, 500), Calls: item.Calls, TotalTimeMS: item.TotalTimeMS, MeanTimeMS: item.MeanTimeMS, Rows: item.Rows, SharedBlocksHit: item.SharedBlocksHit, SharedBlocksRead: item.SharedBlocksRead, TempBlocksRead: item.TempBlocksRead, TempBlocksWritten: item.TempBlocksWritten, WALBytes: item.WALBytes})
	}
	return result, nil
}

func (r *Runtime) Replication(ctx context.Context, id string, slots bool) (inspector.ReplicationResult, error) {
	release, err := r.acquire(ctx, id)
	if err != nil {
		return inspector.ReplicationResult{}, err
	}
	defer release()
	_, cfg, err := r.connectionConfig(ctx, id)
	if err != nil {
		return inspector.ReplicationResult{}, err
	}
	return inspector.Replication(ctx, cfg, slots)
}
func (r *Runtime) AIReplication(ctx context.Context, ref string, slots bool) (AIReplicationResult, error) {
	source, err := r.resolveInstanceRef(ctx, ref)
	if err != nil {
		return AIReplicationResult{}, err
	}
	release, err := r.acquire(ctx, source.ID)
	if err != nil {
		return AIReplicationResult{}, err
	}
	defer release()
	_, cfg, err := r.connectionConfig(ctx, source.ID)
	if err != nil {
		return AIReplicationResult{}, err
	}
	raw, err := inspector.Replication(ctx, cfg, slots)
	if err != nil {
		return AIReplicationResult{}, err
	}
	result := AIReplicationResult{AICommon: common(ref, "live", time.Now().UTC(), false), Role: raw.Role, ReceiveLSN: raw.ReceiveLSN, ReplayLSN: raw.ReplayLSN, ReplayLagBytes: raw.ReplayLagBytes, ReplayLagBytesAvailable: raw.ReplayLagBytesAvailable, LastReplayAgeSeconds: raw.LastReplayAgeSeconds, Connections: []AIReplicationConnection{}}
	for _, item := range raw.Connections {
		result.Connections = append(result.Connections, AIReplicationConnection{StandbyRef: anonymousID(ref, "standby", fmt.Sprint(item.PID)), State: item.State, SyncState: item.SyncState, SentLSN: item.SentLSN, WriteLSN: item.WriteLSN, FlushLSN: item.FlushLSN, ReplayLSN: item.ReplayLSN, ReplayLagBytes: item.ReplayLagBytes, ReplayLagBytesAvailable: item.ReplayLagBytesAvailable, ReplayLagSeconds: item.ReplayLagSeconds})
	}
	for _, item := range raw.Slots {
		result.Slots = append(result.Slots, AIReplicationSlot{SlotRef: anonymousID(ref, "slot", item.Name), SlotType: item.SlotType, Active: item.Active, RetainedBytes: item.RetainedBytes})
	}
	return result, nil
}

func (r *Runtime) AIExplainSafe(ctx context.Context, ref, sql, format string) (AIExplainResult, error) {
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "text" {
		return AIExplainResult{}, &datasource.PublicError{Code: "INVALID_FORMAT", Message: "explain format must be json or text"}
	}
	raw, err := r.AIExplain(ctx, ref, sql)
	if err != nil {
		return AIExplainResult{}, err
	}
	objects := make([]AIExplainObject, 0, 16)
	planning := extractExplainSummary(raw.Plan, &objects)
	result := AIExplainResult{AICommon: common(ref, "live", time.Now().UTC(), false), Format: format, Plan: raw.Plan, PlanningTimeMS: planning, Objects: objects}
	if format == "text" {
		encoded, marshalErr := json.MarshalIndent(raw.Plan, "", "  ")
		if marshalErr != nil {
			return AIExplainResult{}, fmt.Errorf("encode explain text: %w", marshalErr)
		}
		result.Plan = string(encoded)
	}
	return result, nil
}

func extractExplainSummary(value any, objects *[]AIExplainObject) *float64 {
	var planning *float64
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if value, ok := typed["Planning Time"].(float64); ok {
				copy := value
				planning = &copy
			}
			if nodeType, ok := typed["Node Type"].(string); ok && len(*objects) < 50 {
				item := AIExplainObject{NodeType: nodeType}
				item.SchemaName, _ = typed["Schema"].(string)
				item.RelationName, _ = typed["Relation Name"].(string)
				item.IndexName, _ = typed["Index Name"].(string)
				item.EstimatedRows, _ = typed["Plan Rows"].(float64)
				item.TotalCost, _ = typed["Total Cost"].(float64)
				*objects = append(*objects, item)
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return planning
}
