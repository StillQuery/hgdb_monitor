package service

import (
	"context"
	"time"

	"hgdb_monitor/core/internal/model"
)

type AIDiagnoseConnectionFacts struct {
	Current           int64    `json:"current"`
	Maximum           int64    `json:"maximum"`
	UtilizationRatio  *float64 `json:"utilizationRatio,omitempty"`
	Active            int64    `json:"active"`
	Waiting           int64    `json:"waiting"`
	IdleInTransaction int64    `json:"idleInTransaction"`
}

type AIDiagnoseActivityFacts struct {
	QueriesOver60Seconds       int64 `json:"queriesOver60Seconds"`
	TransactionsOver300Seconds int64 `json:"transactionsOver300Seconds"`
}

type AIDiagnoseBlockingFacts struct {
	BlockedSessions int64 `json:"blockedSessions"`
	UngrantedLocks  int64 `json:"ungrantedLocks"`
}

type AIDiagnoseReplicationFacts struct {
	Connections   int64   `json:"connections"`
	MaxLagBytes   int64   `json:"maxLagBytes"`
	MaxLagSeconds float64 `json:"maxLagSeconds"`
}

type AIDiagnoseCurrentFacts struct {
	Connected   bool                       `json:"connected"`
	Role        string                     `json:"role"`
	Connections AIDiagnoseConnectionFacts  `json:"connections"`
	Activity    AIDiagnoseActivityFacts    `json:"activity"`
	Blocking    AIDiagnoseBlockingFacts    `json:"blocking"`
	Replication AIDiagnoseReplicationFacts `json:"replication"`
	Rates       *OverviewDeltas            `json:"rates,omitempty"`
}

type AIDiagnoseObservation struct {
	Code     string             `json:"code"`
	Rule     string             `json:"rule"`
	Evidence map[string]float64 `json:"evidence"`
}

type AIDiagnoseCapabilities struct {
	History        bool `json:"history"`
	Sessions       bool `json:"sessions"`
	Blocking       bool `json:"blocking"`
	TopSQL         bool `json:"topSql"`
	Replication    bool `json:"replication"`
	EstimatedPlans bool `json:"estimatedPlans"`
}

type AIDiagnoseMetricComparison struct {
	Current     MetricSummary  `json:"current"`
	Previous    *MetricSummary `json:"previous,omitempty"`
	ChangeRatio *float64       `json:"changeRatio,omitempty"`
}

type AIDiagnoseResult struct {
	AICommon
	Focus               string                                `json:"focus"`
	WindowMinutes       int                                   `json:"windowMinutes"`
	ComparisonRequested bool                                  `json:"comparisonRequested"`
	ComparisonAvailable bool                                  `json:"comparisonAvailable"`
	CurrentFacts        AIDiagnoseCurrentFacts                `json:"currentFacts"`
	RecentChanges       map[string]AIDiagnoseMetricComparison `json:"recentChanges,omitempty"`
	Observations        []AIDiagnoseObservation               `json:"observations"`
	AvailableDetails    AIDiagnoseCapabilities                `json:"availableDetails"`
}

var diagnoseMetrics = map[string][]string{
	"overall":     {"sessions_total", "sessions_active", "sessions_waiting", "commits", "rollbacks", "blocked_sessions", "long_queries", "long_transactions"},
	"performance": {"commits", "rollbacks", "blocks_hit", "blocks_read", "rows_inserted", "rows_updated", "rows_deleted", "temp_bytes"},
	"connections": {"sessions_total", "sessions_active", "sessions_waiting", "sessions_idle_transaction", "long_transactions"},
	"blocking":    {"blocked_sessions", "locks_not_granted", "deadlocks", "long_transactions"},
	"replication": {"replication_connections", "max_replay_lag_bytes", "max_replay_lag_seconds", "wal_bytes"},
}

// AIDiagnoseDatabase composes live facts and bounded history for common user
// questions. It deliberately reports evidence and rules without assigning a
// health score or an inferred severity.
func (r *Runtime) AIDiagnoseDatabase(ctx context.Context, instanceRef, focus string, windowMinutes int, compare bool) (AIDiagnoseResult, error) {
	if focus == "" {
		focus = "overall"
	}
	metrics, ok := diagnoseMetrics[focus]
	if !ok {
		return AIDiagnoseResult{}, &PublicError{Code: "INVALID_FOCUS", Message: "diagnosis focus is invalid"}
	}
	if windowMinutes == 0 {
		windowMinutes = 30
	}
	if windowMinutes < 5 || windowMinutes > 120 || (compare && windowMinutes > 60) {
		return AIDiagnoseResult{}, &PublicError{Code: "INVALID_HISTORY_RANGE", Message: "diagnosis history range is invalid"}
	}
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return AIDiagnoseResult{}, err
	}
	overview, err := r.AIOverview(ctx, instanceRef, true)
	if err != nil {
		return AIDiagnoseResult{}, err
	}
	snapshot := overview.Overview
	connectionFacts := AIDiagnoseConnectionFacts{Current: snapshot.SessionsTotal, Maximum: snapshot.MaxConnections,
		Active: snapshot.SessionsActive, Waiting: snapshot.SessionsWaiting, IdleInTransaction: snapshot.SessionsIdleTransaction}
	if snapshot.MaxConnections > 0 {
		ratio := float64(snapshot.SessionsTotal) / float64(snapshot.MaxConnections)
		connectionFacts.UtilizationRatio = &ratio
	}
	result := AIDiagnoseResult{
		AICommon: overview.AICommon, Focus: focus, WindowMinutes: windowMinutes, ComparisonRequested: compare,
		CurrentFacts: AIDiagnoseCurrentFacts{
			Connected: snapshot.Connected, Role: model.RoleFromRecovery(snapshot.InRecovery), Connections: connectionFacts,
			Activity:    AIDiagnoseActivityFacts{QueriesOver60Seconds: snapshot.LongQueries, TransactionsOver300Seconds: snapshot.LongTransactions},
			Blocking:    AIDiagnoseBlockingFacts{BlockedSessions: snapshot.BlockedSessions, UngrantedLocks: snapshot.LocksNotGranted},
			Replication: AIDiagnoseReplicationFacts{Connections: snapshot.ReplicationConnections, MaxLagBytes: snapshot.MaxReplayLagBytes, MaxLagSeconds: snapshot.MaxReplayLagSeconds},
			Rates:       overview.Deltas,
		},
		Observations: diagnoseObservations(snapshot),
		AvailableDetails: AIDiagnoseCapabilities{History: source.PersistentEnabled, Sessions: source.Capabilities.Sessions,
			Blocking: source.Capabilities.Blocking, TopSQL: source.Capabilities.PGStatStatements,
			Replication: source.Capabilities.Replication, EstimatedPlans: true},
	}
	result.Source = "live"
	if !source.PersistentEnabled {
		result.Warnings = append(result.Warnings, "HISTORY_DISABLED")
		return result, nil
	}
	history, err := r.AIQueryMetrics(ctx, instanceRef, metrics, windowMinutes, time.Time{}, compare)
	if err != nil {
		return AIDiagnoseResult{}, err
	}
	result.Source = "live+history"
	result.RecentChanges = make(map[string]AIDiagnoseMetricComparison, len(history.Metrics))
	for key, series := range history.Metrics {
		result.RecentChanges[key] = AIDiagnoseMetricComparison{Current: series.Summary, Previous: series.Previous, ChangeRatio: series.ChangeRatio}
	}
	result.ComparisonAvailable = compare && history.CurrentPointCount > 0 && history.PreviousPointCount > 0
	result.Warnings = append(result.Warnings, history.Warnings...)
	if compare && !result.ComparisonAvailable {
		result.Warnings = append(result.Warnings, "PREVIOUS_WINDOW_DATA_UNAVAILABLE")
	}
	return result, nil
}

func diagnoseObservations(snapshot model.Snapshot) []AIDiagnoseObservation {
	observations := make([]AIDiagnoseObservation, 0, 4)
	if snapshot.BlockedSessions > 0 {
		observations = append(observations, AIDiagnoseObservation{Code: "BLOCKING_PRESENT", Rule: "blockedSessions > 0", Evidence: map[string]float64{"blockedSessions": float64(snapshot.BlockedSessions)}})
	}
	if snapshot.SessionsWaiting > 0 {
		observations = append(observations, AIDiagnoseObservation{Code: "WAITING_SESSIONS_PRESENT", Rule: "sessionsWaiting > 0", Evidence: map[string]float64{"sessionsWaiting": float64(snapshot.SessionsWaiting)}})
	}
	if snapshot.LongQueries > 0 {
		observations = append(observations, AIDiagnoseObservation{Code: "LONG_QUERY_PRESENT", Rule: "queryAgeSeconds >= 60", Evidence: map[string]float64{"matchingSessions": float64(snapshot.LongQueries), "thresholdSeconds": 60}})
	}
	if snapshot.LongTransactions > 0 {
		observations = append(observations, AIDiagnoseObservation{Code: "LONG_TRANSACTION_PRESENT", Rule: "transactionAgeSeconds >= 300", Evidence: map[string]float64{"matchingSessions": float64(snapshot.LongTransactions), "thresholdSeconds": 300}})
	}
	return observations
}
