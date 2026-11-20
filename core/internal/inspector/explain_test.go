package inspector

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizePlanKeepsObjectNamesAndRedactsExpressions(t *testing.T) {
	input := map[string]any{
		"Relation Name": "orders_2026",
		"Filter":        "((email = 'person@example.com'::text) AND (amount > 100))",
		"Plans":         []any{map[string]any{"Index Name": "orders_email_idx", "Index Cond": "email = 'person@example.com'::text"}},
	}
	result := sanitizePlan(input, "")
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, "orders_2026") || !strings.Contains(text, "orders_email_idx") {
		t.Fatalf("object names missing: %s", text)
	}
	if strings.Contains(text, "person@example.com") || strings.Contains(text, "100") {
		t.Fatalf("plan literals not redacted: %s", text)
	}
}
