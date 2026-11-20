package collector

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSnapshotJSONContainsNoConnectionIdentity(t *testing.T) {
	encoded, err := json.Marshal(Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"host", "address", "password", "username", "database"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("snapshot JSON contains forbidden connection identity %q: %s", forbidden, text)
		}
	}
}

func TestBuiltinRegistryDeclaresTwentyFourMetrics(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterBuiltins(registry); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, definition := range registry.List() {
		count += len(definition.Metrics)
	}
	if count != 24 {
		t.Fatalf("expected 24 historical metrics, got %d", count)
	}
}

func TestSnapshotUsesClientConnectionAndLagAvailabilitySemantics(t *testing.T) {
	for _, required := range []string{
		"backend_type = 'client backend'",
		"pid <> pg_backend_pid()",
		"pg_last_wal_receive_lsn()",
		"activity.backend_type = 'client backend'",
		"max_replay_lag_bytes_available",
		"max_replay_lag_seconds_available",
	} {
		if !strings.Contains(snapshotSQL, required) {
			t.Fatalf("snapshot query is missing %q", required)
		}
	}
}
