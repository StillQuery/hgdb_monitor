package service

import (
	"testing"

	"hgdb_monitor/core/internal/model"
)

func TestReplicationLagMetricsRespectAvailability(t *testing.T) {
	bytesDefinition := historyMetrics["max_replay_lag_bytes"]
	secondsDefinition := historyMetrics["max_replay_lag_seconds"]
	missing := model.Snapshot{}
	if metricValueAvailable(bytesDefinition, missing) || metricValueAvailable(secondsDefinition, missing) {
		t.Fatal("unavailable replication lag must not be treated as zero")
	}
	available := model.Snapshot{MaxReplayLagBytesAvailable: true, MaxReplayLagSecondsAvailable: true}
	if !metricValueAvailable(bytesDefinition, available) || !metricValueAvailable(secondsDefinition, available) {
		t.Fatal("available replication lag was rejected")
	}
}
