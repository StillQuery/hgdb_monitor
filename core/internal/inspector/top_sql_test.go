package inspector

import "testing"

func TestExtensionRelationUsesDetectedSchema(t *testing.T) {
	if got := extensionRelation("monitoring", "pg_stat_statements_info"); got != `"monitoring"."pg_stat_statements_info"` {
		t.Fatalf("unexpected extension relation: %s", got)
	}
}
