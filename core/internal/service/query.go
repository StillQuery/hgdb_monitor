package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"hgdb_monitor/core/internal/model"
	"hgdb_monitor/core/internal/storage"
)

type HistoryResult struct {
	From   time.Time        `json:"from"`
	To     time.Time        `json:"to"`
	Points []model.Snapshot `json:"points"`
}

type AIInstance struct {
	InstanceRef    string     `json:"instanceRef"`
	InstanceName   string     `json:"instanceName"`
	InstanceAlias  string     `json:"instanceAlias"`
	Status         string     `json:"status"`
	Role           string     `json:"role"`
	HistoryEnabled bool       `json:"historyEnabled"`
	LastSampleAt   *time.Time `json:"lastSampleAt,omitempty"`
}

type MetricPoint struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}
type MetricSeries struct {
	Points      []MetricPoint  `json:"points"`
	Summary     MetricSummary  `json:"summary"`
	Previous    *MetricSummary `json:"previous,omitempty"`
	ChangeRatio *float64       `json:"changeRatio,omitempty"`
}
type AIQueryMetricsResult struct {
	AICommon
	From               time.Time               `json:"from"`
	To                 time.Time               `json:"to"`
	PreviousFrom       *time.Time              `json:"previousFrom,omitempty"`
	PreviousTo         *time.Time              `json:"previousTo,omitempty"`
	Metrics            map[string]MetricSeries `json:"metrics"`
	CurrentPointCount  int                     `json:"currentPointCount"`
	PreviousPointCount int                     `json:"previousPointCount,omitempty"`
	ActualFrom         *time.Time              `json:"actualFrom,omitempty"`
	ActualTo           *time.Time              `json:"actualTo,omitempty"`
}

type MetricSummary struct {
	Kind          string  `json:"kind"`
	Unit          string  `json:"unit"`
	Start         float64 `json:"start"`
	End           float64 `json:"end"`
	Minimum       float64 `json:"minimum,omitempty"`
	Maximum       float64 `json:"maximum,omitempty"`
	Average       float64 `json:"average,omitempty"`
	Increase      float64 `json:"increase,omitempty"`
	RatePerSecond float64 `json:"ratePerSecond,omitempty"`
}

type AIHistorySummary struct {
	InstanceRef string                   `json:"instanceRef"`
	Label       string                   `json:"label"`
	From        time.Time                `json:"from"`
	To          time.Time                `json:"to"`
	PointCount  int                      `json:"pointCount"`
	Metrics     map[string]MetricSummary `json:"metrics"`
}

type metricDefinition struct {
	kind  string
	unit  string
	value func(model.Snapshot) float64
}

var historyMetrics = map[string]metricDefinition{
	"sessions_total":            {"gauge", "connections", func(s model.Snapshot) float64 { return float64(s.SessionsTotal) }},
	"sessions_active":           {"gauge", "connections", func(s model.Snapshot) float64 { return float64(s.SessionsActive) }},
	"sessions_idle_transaction": {"gauge", "connections", func(s model.Snapshot) float64 { return float64(s.SessionsIdleTransaction) }},
	"sessions_waiting":          {"gauge", "connections", func(s model.Snapshot) float64 { return float64(s.SessionsWaiting) }},
	"long_queries":              {"gauge", "queries", func(s model.Snapshot) float64 { return float64(s.LongQueries) }},
	"long_transactions":         {"gauge", "transactions", func(s model.Snapshot) float64 { return float64(s.LongTransactions) }},
	"commits":                   {"counter", "transactions", func(s model.Snapshot) float64 { return float64(s.Commits) }},
	"rollbacks":                 {"counter", "transactions", func(s model.Snapshot) float64 { return float64(s.Rollbacks) }},
	"blocks_hit":                {"counter", "blocks", func(s model.Snapshot) float64 { return float64(s.BlocksHit) }},
	"blocks_read":               {"counter", "blocks", func(s model.Snapshot) float64 { return float64(s.BlocksRead) }},
	"rows_inserted":             {"counter", "rows", func(s model.Snapshot) float64 { return float64(s.RowsInserted) }},
	"rows_updated":              {"counter", "rows", func(s model.Snapshot) float64 { return float64(s.RowsUpdated) }},
	"rows_deleted":              {"counter", "rows", func(s model.Snapshot) float64 { return float64(s.RowsDeleted) }},
	"temp_files":                {"counter", "files", func(s model.Snapshot) float64 { return float64(s.TempFiles) }},
	"temp_bytes":                {"counter", "bytes", func(s model.Snapshot) float64 { return float64(s.TempBytes) }},
	"deadlocks":                 {"counter", "events", func(s model.Snapshot) float64 { return float64(s.Deadlocks) }},
	"locks_not_granted":         {"gauge", "locks", func(s model.Snapshot) float64 { return float64(s.LocksNotGranted) }},
	"blocked_sessions":          {"gauge", "connections", func(s model.Snapshot) float64 { return float64(s.BlockedSessions) }},
	"wal_bytes":                 {"counter", "bytes", func(s model.Snapshot) float64 { return float64(s.WALBytes) }},
	"checkpoints_timed":         {"counter", "checkpoints", func(s model.Snapshot) float64 { return float64(s.CheckpointsTimed) }},
	"checkpoints_requested":     {"counter", "checkpoints", func(s model.Snapshot) float64 { return float64(s.CheckpointsRequested) }},
	"replication_connections":   {"gauge", "connections", func(s model.Snapshot) float64 { return float64(s.ReplicationConnections) }},
	"max_replay_lag_bytes":      {"gauge", "bytes", func(s model.Snapshot) float64 { return float64(s.MaxReplayLagBytes) }},
	"max_replay_lag_seconds":    {"gauge", "seconds", func(s model.Snapshot) float64 { return s.MaxReplayLagSeconds }},
}

func (r *Runtime) QueryHistory(ctx context.Context, id string, from, to time.Time) (HistoryResult, error) {
	if _, err := r.repo.GetDatasource(ctx, id); err != nil {
		return HistoryResult{}, mapStoreError(err)
	}
	from, to, err := validateHistoryRange(from, to)
	if err != nil {
		return HistoryResult{}, err
	}
	points, err := r.repo.QuerySnapshots(ctx, id, from, to)
	if err != nil {
		return HistoryResult{}, err
	}
	return HistoryResult{From: from, To: to, Points: points}, nil
}

// Latest returns the newest persisted sample for the local management page.
// Unlike the AI-facing variant, this resolves the internal datasource id and
// therefore must only be exposed through the loopback management API.
func (r *Runtime) Latest(ctx context.Context, id string) (model.Snapshot, error) {
	if _, err := r.repo.GetDatasource(ctx, id); err != nil {
		return model.Snapshot{}, mapStoreError(err)
	}
	snapshot, err := r.repo.LatestSnapshot(ctx, id)
	if errors.Is(err, storage.ErrSnapshotNotFound) {
		return model.Snapshot{}, &PublicError{Code: "NO_MONITORING_DATA", Message: "no monitoring sample is available for this instance"}
	}
	if err == nil {
		if status, statusErr := r.repo.LatestInstanceStatus(ctx, id); statusErr == nil {
			model.ApplyStatus(&snapshot, status)
		} else if !errors.Is(statusErr, storage.ErrStatusNotFound) {
			return model.Snapshot{}, statusErr
		}
	}
	return snapshot, err
}

func (r *Runtime) ListAIInstances(ctx context.Context) ([]AIInstance, error) {
	sources, err := r.repo.ListDatasources(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]AIInstance, 0, len(sources))
	for _, source := range sources {
		item := AIInstance{InstanceRef: source.InstanceRef, InstanceName: source.DisplayName, InstanceAlias: safeInstanceLabel(source.InstanceRef),
			Status: "unknown", Role: model.RoleUnknown, HistoryEnabled: source.PersistentEnabled}
		jobStatus := r.scheduler.Status(source.ID)
		if snapshot, snapshotErr := r.repo.LatestSnapshot(ctx, source.ID); snapshotErr == nil {
			if jobStatus.LastError != "" {
				item.Status = "offline"
			} else if time.Since(snapshot.CollectedAt) <= 2*StatusInterval {
				item.Status = "online"
			}
			if status, statusErr := r.repo.LatestInstanceStatus(ctx, source.ID); statusErr == nil {
				item.Role = model.RoleFromRecovery(status.InRecovery)
			} else if snapshot.InRecovery {
				item.Role = model.RoleStandby
			} else if r.collectStatus == nil {
				item.Role = model.RoleReadWrite
			}
			last := snapshot.LastCollectedAt
			item.LastSampleAt = &last
		} else if !errors.Is(snapshotErr, storage.ErrSnapshotNotFound) {
			return nil, snapshotErr
		} else if jobStatus.LastError != "" {
			item.Status = "offline"
		} else if source.VerifiedAt != nil && time.Since(*source.VerifiedAt) <= StatusInterval {
			item.Status = "online"
		}
		result = append(result, item)
	}
	return result, nil
}

func (r *Runtime) AIQueryMetrics(ctx context.Context, instanceRef string, selected []string, windowMinutes int, end time.Time, compare bool) (AIQueryMetricsResult, error) {
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return AIQueryMetricsResult{}, err
	}
	if !source.PersistentEnabled {
		return AIQueryMetricsResult{}, &PublicError{Code: "HISTORY_DISABLED", Message: "history collection is disabled"}
	}
	if windowMinutes < 5 || windowMinutes > 120 || (compare && windowMinutes > 60) {
		return AIQueryMetricsResult{}, &PublicError{Code: "INVALID_HISTORY_RANGE", Message: "history range is invalid"}
	}
	if len(selected) < 1 || len(selected) > 8 {
		return AIQueryMetricsResult{}, &PublicError{Code: "INVALID_METRICS", Message: "select between one and eight metrics"}
	}
	definitions, err := selectMetrics(selected)
	if err != nil {
		return AIQueryMetricsResult{}, err
	}
	now := time.Now().UTC()
	if end.IsZero() {
		end = now
	} else {
		end = end.UTC()
	}
	if end.After(now.Add(SampleInterval)) || end.Before(now.Add(-HistoryWindow)) {
		return AIQueryMetricsResult{}, &PublicError{Code: "INVALID_HISTORY_RANGE", Message: "history end must be within the last two hours"}
	}
	window := time.Duration(windowMinutes) * time.Minute
	from := end.Add(-window)
	queryFrom := from
	var previousFrom, previousTo *time.Time
	if compare {
		pf := from.Add(-window)
		pt := from
		previousFrom = &pf
		previousTo = &pt
		queryFrom = pf
	}
	if end.Sub(queryFrom) > HistoryWindow {
		return AIQueryMetricsResult{}, &PublicError{Code: "INVALID_HISTORY_RANGE", Message: "history range exceeds the last two hours"}
	}
	points, err := r.repo.QuerySnapshots(ctx, source.ID, queryFrom, end)
	if err != nil {
		return AIQueryMetricsResult{}, err
	}
	current := make([]model.Snapshot, 0, len(points))
	previous := make([]model.Snapshot, 0, len(points))
	for _, point := range points {
		if point.CollectedAt.Before(from) {
			previous = append(previous, point)
		} else {
			current = append(current, point)
		}
	}
	observed := end
	if len(current) > 0 {
		observed = current[len(current)-1].CollectedAt
	}
	result := AIQueryMetricsResult{AICommon: common(instanceRef, "history", observed, false), From: from, To: end, PreviousFrom: previousFrom, PreviousTo: previousTo, Metrics: make(map[string]MetricSeries, len(definitions)), CurrentPointCount: len(current), PreviousPointCount: len(previous)}
	if len(current) > 0 {
		a, b := current[0].CollectedAt, current[len(current)-1].CollectedAt
		result.ActualFrom = &a
		result.ActualTo = &b
	}
	for name, definition := range definitions {
		series := MetricSeries{Summary: summarizeMetric(definition, current), Points: metricPoints(definition, current, 240)}
		if len(current) > 240 {
			result.Truncated = true
		}
		if compare {
			summary := summarizeMetric(definition, previous)
			series.Previous = &summary
			currentValue, previousValue := comparisonValue(series.Summary), comparisonValue(summary)
			if previousValue != 0 {
				ratio := (currentValue - previousValue) / previousValue
				series.ChangeRatio = &ratio
			}
		}
		result.Metrics[name] = series
	}
	if len(current) == 0 {
		result.Warnings = append(result.Warnings, "NO_HISTORY_DATA")
	}
	return result, nil
}

func metricPoints(definition metricDefinition, points []model.Snapshot, maximum int) []MetricPoint {
	step := 1
	if len(points) > maximum {
		step = int(math.Ceil(float64(len(points)) / float64(maximum)))
	}
	result := make([]MetricPoint, 0, min(len(points), maximum))
	for index, point := range points {
		if index%step == 0 || index == len(points)-1 {
			result = append(result, MetricPoint{At: point.CollectedAt, Value: definition.value(point)})
		}
	}
	return result
}
func comparisonValue(summary MetricSummary) float64 {
	if summary.Kind == "counter" {
		return summary.Increase
	}
	return summary.Average
}

func (r *Runtime) AIHistory(ctx context.Context, instanceRef string, minutes int, selected []string) (AIHistorySummary, error) {
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return AIHistorySummary{}, err
	}
	if minutes <= 0 || minutes > 120 {
		return AIHistorySummary{}, &PublicError{Code: "INVALID_HISTORY_RANGE", Message: "history duration must be between 1 and 120 minutes"}
	}
	definitions, err := selectMetrics(selected)
	if err != nil {
		return AIHistorySummary{}, err
	}
	to := time.Now().UTC()
	from := to.Add(-time.Duration(minutes) * time.Minute)
	points, err := r.repo.QuerySnapshots(ctx, source.ID, from, to)
	if err != nil {
		return AIHistorySummary{}, err
	}
	result := AIHistorySummary{InstanceRef: source.InstanceRef, Label: safeInstanceLabel(source.InstanceRef),
		From: from, To: to, PointCount: len(points), Metrics: make(map[string]MetricSummary, len(definitions))}
	for name, definition := range definitions {
		result.Metrics[name] = summarizeMetric(definition, points)
	}
	return result, nil
}

func (r *Runtime) AILatest(ctx context.Context, instanceRef string) (model.Snapshot, error) {
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot, err := r.repo.LatestSnapshot(ctx, source.ID)
	if errors.Is(err, storage.ErrSnapshotNotFound) {
		return model.Snapshot{}, &PublicError{Code: "NO_MONITORING_DATA", Message: "no monitoring sample is available for this instance"}
	}
	if err == nil {
		if status, statusErr := r.repo.LatestInstanceStatus(ctx, source.ID); statusErr == nil {
			model.ApplyStatus(&snapshot, status)
		} else if !errors.Is(statusErr, storage.ErrStatusNotFound) {
			return model.Snapshot{}, statusErr
		}
	}
	return snapshot, err
}

func validateHistoryRange(from, to time.Time) (time.Time, time.Time, error) {
	from, to = from.UTC(), to.UTC()
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return time.Time{}, time.Time{}, &PublicError{Code: "INVALID_HISTORY_RANGE", Message: "history start must be earlier than history end"}
	}
	if to.Sub(from) > HistoryWindow {
		return time.Time{}, time.Time{}, &PublicError{Code: "INVALID_HISTORY_RANGE", Message: "history range cannot exceed two hours"}
	}
	return from, to, nil
}

func (r *Runtime) resolveInstanceRef(ctx context.Context, instanceRef string) (model.Datasource, error) {
	if !strings.HasPrefix(instanceRef, "db_") || len(instanceRef) != 29 {
		return model.Datasource{}, &PublicError{Code: "INVALID_INSTANCE_REF", Message: "instance reference is invalid"}
	}
	sources, err := r.repo.ListDatasources(ctx)
	if err != nil {
		return model.Datasource{}, err
	}
	for _, source := range sources {
		if source.InstanceRef == instanceRef {
			return source, nil
		}
	}
	return model.Datasource{}, &PublicError{Code: "INSTANCE_NOT_FOUND", Message: "monitoring instance does not exist"}
}

func safeInstanceLabel(instanceRef string) string {
	if len(instanceRef) < 6 {
		return "实例"
	}
	return "实例-" + instanceRef[len(instanceRef)-6:]
}

func selectMetrics(selected []string) (map[string]metricDefinition, error) {
	if len(selected) == 0 {
		return historyMetrics, nil
	}
	if len(selected) > 12 {
		return nil, &PublicError{Code: "TOO_MANY_METRICS", Message: "at most twelve metrics can be queried at once"}
	}
	result := make(map[string]metricDefinition, len(selected))
	for _, name := range selected {
		definition, ok := historyMetrics[name]
		if !ok {
			return nil, &PublicError{Code: "UNKNOWN_METRIC", Message: fmt.Sprintf("metric %q is not supported", name)}
		}
		result[name] = definition
	}
	return result, nil
}

func summarizeMetric(definition metricDefinition, points []model.Snapshot) MetricSummary {
	result := MetricSummary{Kind: definition.kind, Unit: definition.unit}
	if len(points) == 0 {
		return result
	}
	result.Start = definition.value(points[0])
	result.End = definition.value(points[len(points)-1])
	if definition.kind == "counter" {
		for index := 1; index < len(points); index++ {
			previous := definition.value(points[index-1])
			current := definition.value(points[index])
			if current >= previous {
				result.Increase += current - previous
			} else {
				result.Increase += current
			}
		}
		seconds := points[len(points)-1].CollectedAt.Sub(points[0].CollectedAt).Seconds()
		if seconds > 0 {
			result.RatePerSecond = result.Increase / seconds
		}
		return result
	}
	result.Minimum, result.Maximum = math.Inf(1), math.Inf(-1)
	for _, point := range points {
		value := definition.value(point)
		result.Average += value
		result.Minimum = math.Min(result.Minimum, value)
		result.Maximum = math.Max(result.Maximum, value)
	}
	result.Average /= float64(len(points))
	return result
}
