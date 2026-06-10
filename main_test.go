package main

import "testing"

// TestCountAccounts verifies that bookable accounts are counted as either leaf
// segments (no child segments) or nodes explicitly marked with ".self".
func TestCountAccounts(t *testing.T) {
	chart := map[string]any{
		// Leaf with metadata only -> 1 bookable.
		"wallet": map[string]any{
			".metadata": map[string]any{},
		},
		// Intermediate node (.pattern, no .self) with two leaf children -> 2.
		"users": map[string]any{
			".pattern": "x",
			"alice":    map[string]any{".metadata": map[string]any{}},
			"bob":      map[string]any{},
		},
		// ".self" on a node that also has a child leaf -> node + child = 2.
		"platform": map[string]any{
			".self": map[string]any{},
			"fees":  map[string]any{".metadata": map[string]any{}},
		},
		// Nil-bodied segment is a leaf -> 1.
		"world": nil,
		// Top-level dot-keys are not segments and must be ignored.
		".meta": "ignored",
	}

	got := countAccounts(chart)
	want := 1 + 2 + 2 + 1 // wallet + (alice, bob) + (platform, fees) + world
	if got != want {
		t.Fatalf("countAccounts = %d, want %d", got, want)
	}
}
