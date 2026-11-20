package inspector

import (
	"strings"
	"testing"
)

func TestActivityQueryAvoidsOptionalQueryIDColumn(t *testing.T) {
	query, err := buildActivityQuery(ActivityActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(query), "query_id") {
		t.Fatal("activity detail query must not depend on the optional pg_stat_activity.query_id column")
	}
	if !strings.Contains(query, "pg_catalog.pg_stat_activity") || !strings.Contains(query, "LIMIT $1") {
		t.Fatalf("activity query lost required clauses: %s", query)
	}
	if !strings.Contains(query, "backend_type = 'client backend'") {
		t.Fatalf("activity query must only return client sessions: %s", query)
	}
	for _, placeholder := range []string{"$1", "$2::integer", "$3::text"} {
		if !strings.Contains(query, placeholder) {
			t.Fatalf("active-session query does not bind %s: %s", placeholder, query)
		}
	}
}

func TestLockWaitQueryCorrelatesBlockingEdgeToExactLockTag(t *testing.T) {
	query := lockWaitQuery
	for _, fragment := range []string{
		"SELECT DISTINCT blocker_pid",
		"blockers.blocker_pid = 0 AND holding.pid IS NULL",
		"holding.database IS NOT DISTINCT FROM waiting.database",
		"holding.relation IS NOT DISTINCT FROM waiting.relation",
		"holding.transactionid IS NOT DISTINCT FROM waiting.transactionid",
		"LEFT JOIN pg_catalog.pg_stat_activity blocker",
		"'prepared_transaction'",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("lock-wait query must contain %q: %s", fragment, query)
		}
	}
	if strings.Contains(query, "clock_timestamp() - coalesce(blocked.state_change") {
		t.Fatal("query age must not be presented as an exact lock-wait duration")
	}
}
