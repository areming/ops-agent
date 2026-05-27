package tools

import "testing"

func TestRegistryGetAndOrder(t *testing.T) {
	r := NewRegistry(Shell{}, ReadFile{}, WriteFile{})

	for _, name := range []string{"run_command", "read_file", "write_file"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("Get(%q) = not found, want found", name)
		}
	}

	if _, ok := r.Get("nope"); ok {
		t.Errorf("Get(%q) = found, want not found", "nope")
	}

	list := r.List()
	if len(list) != 3 {
		t.Fatalf("List() len = %d, want 3", len(list))
	}
	// List preserves construction order.
	want := []string{"run_command", "read_file", "write_file"}
	for i, tool := range list {
		if tool.Name() != want[i] {
			t.Errorf("List()[%d].Name() = %q, want %q", i, tool.Name(), want[i])
		}
	}
}

func TestRegistryEmpty(t *testing.T) {
	r := NewRegistry()
	if got := len(r.List()); got != 0 {
		t.Errorf("empty registry List() len = %d, want 0", got)
	}
	if _, ok := r.Get("anything"); ok {
		t.Errorf("empty registry Get returned ok, want not found")
	}
}
