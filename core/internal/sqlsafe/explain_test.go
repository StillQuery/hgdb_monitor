package sqlsafe

import "testing"

func TestNormalizeExplainInputAcceptsOneReadStatement(t *testing.T) {
	cases := []string{
		"SELECT * FROM public.orders;",
		"/* lead */ WITH x AS (SELECT ';' AS value) SELECT * FROM x",
		"SELECT $$a;b$$",
	}
	for _, input := range cases {
		if _, err := NormalizeExplainInput(input); err != nil {
			t.Errorf("expected %q to be accepted: %v", input, err)
		}
	}
}

func TestNormalizeExplainInputRejectsUnsafeForms(t *testing.T) {
	cases := []string{
		"EXPLAIN ANALYZE SELECT 1",
		"UPDATE orders SET value = 1",
		"SELECT 1; DELETE FROM orders",
		"SELECT 'unterminated",
		"SELECT 1; SELECT 2",
		"VALUES (1), (2)",
		"TABLE public.orders",
		"WITH changed AS (DELETE FROM orders RETURNING *) SELECT * FROM changed",
		"WITH rows AS (SELECT 1) TABLE rows",
		"SELECT * INTO archived_orders FROM orders",
		"SELECT 'postgres://admin:secret@192.168.1.2/db'",
		"SELECT 1 /* password=secret */",
	}
	for _, input := range cases {
		if _, err := NormalizeExplainInput(input); err == nil {
			t.Errorf("expected %q to be rejected", input)
		}
	}
}
