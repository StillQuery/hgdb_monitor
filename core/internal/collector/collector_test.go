package collector

import "testing"

func TestRegistryRejectsDuplicateAndSorts(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Definition{Name: "z"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Definition{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Definition{Name: "a"}); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	items := registry.List()
	if len(items) != 2 || items[0].Name != "a" || items[1].Name != "z" {
		t.Fatalf("unexpected registry order: %#v", items)
	}
}

func TestBuiltinsRegister(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterBuiltins(registry); err != nil {
		t.Fatal(err)
	}
	if got := len(registry.List()); got != 5 {
		t.Fatalf("expected 5 builtin collector groups, got %d", got)
	}
}
