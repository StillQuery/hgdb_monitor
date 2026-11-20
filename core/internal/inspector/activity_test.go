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
	for _, placeholder := range []string{"$1", "$2::integer", "$3::text"} {
		if !strings.Contains(query, placeholder) {
			t.Fatalf("active-session query does not bind %s: %s", placeholder, query)
		}
	}
}
