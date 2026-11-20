package privacy

import (
	"strings"
	"testing"
)

func TestRedactSQLPreservesObjectsAndRemovesValues(t *testing.T) {
	input := "SELECT customer_id FROM sales.orders WHERE email = 'person@example.com' AND total > 123.45 -- private\nAND token = $$secret$$"
	result := RedactSQL(input)
	if !strings.Contains(result, "sales.orders") || !strings.Contains(result, "customer_id") {
		t.Fatalf("object names were not preserved: %s", result)
	}
	for _, private := range []string{"person@example.com", "123.45", "private", "secret"} {
		if strings.Contains(result, private) {
			t.Fatalf("private literal %q remained in %s", private, result)
		}
	}
}

func TestRedactSQLHandlesEscapedAndNestedComments(t *testing.T) {
	result := RedactSQL("SELECT 'it''s private', table1.col2 /* outer /* inner */ done */ FROM table1")
	if result != "SELECT ?, table1.col2 FROM table1" {
		t.Fatalf("unexpected redaction: %q", result)
	}
}
