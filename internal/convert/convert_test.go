package convert

import (
	"encoding/json"
	"testing"
)

func TestToJSON_BasicMapping(t *testing.T) {
	yaml := []byte(`
name: hello
count: 42
enabled: true
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if m["name"] != "hello" {
		t.Errorf("name = %v, want hello", m["name"])
	}
	// YAML integers are decoded as int by yaml.v3, but json.Marshal turns them into float64 on Unmarshal.
	if m["count"] != float64(42) {
		t.Errorf("count = %v, want 42", m["count"])
	}
	if m["enabled"] != true {
		t.Errorf("enabled = %v, want true", m["enabled"])
	}
}

func TestToJSON_AnchorsAndAliases(t *testing.T) {
	yaml := []byte(`
defaults: &defaults
  timeout: 30
  retries: 3

primary:
  <<: *defaults
  name: primary
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	primary, ok := m["primary"].(map[string]any)
	if !ok {
		t.Fatalf("primary is not a map: %T", m["primary"])
	}

	if primary["timeout"] != float64(30) {
		t.Errorf("primary.timeout = %v, want 30", primary["timeout"])
	}
	if primary["retries"] != float64(3) {
		t.Errorf("primary.retries = %v, want 3", primary["retries"])
	}
	if primary["name"] != "primary" {
		t.Errorf("primary.name = %v, want primary", primary["name"])
	}
}

func TestToJSON_MergeKeyOverride(t *testing.T) {
	yaml := []byte(`
base: &base
  retries: 3
  timeout: 30

override:
  <<: *base
  retries: 5
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	override := m["override"].(map[string]any)
	if override["retries"] != float64(5) {
		t.Errorf("override.retries = %v, want 5 (should override merged value)", override["retries"])
	}
	if override["timeout"] != float64(30) {
		t.Errorf("override.timeout = %v, want 30 (should be inherited from base)", override["timeout"])
	}
}

func TestToJSON_MultiLineLiteral(t *testing.T) {
	yaml := []byte(`
text: |
  line one
  line two
  line three
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	expected := "line one\nline two\nline three\n"
	if m["text"] != expected {
		t.Errorf("text = %q, want %q", m["text"], expected)
	}
}

func TestToJSON_MultiLineFolded(t *testing.T) {
	yaml := []byte(`
text: >
  this is
  a folded
  string
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	expected := "this is a folded string\n"
	if m["text"] != expected {
		t.Errorf("text = %q, want %q", m["text"], expected)
	}
}

func TestToJSON_ExplicitNull(t *testing.T) {
	yaml := []byte(`
a: ~
b: null
c:
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	for _, key := range []string{"a", "b", "c"} {
		if m[key] != nil {
			t.Errorf("%s = %v, want nil", key, m[key])
		}
	}
}

func TestToJSON_NumericTypes(t *testing.T) {
	yaml := []byte(`
integer: 42
negative: -7
float: 3.14
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if m["integer"] != float64(42) {
		t.Errorf("integer = %v, want 42", m["integer"])
	}
	if m["negative"] != float64(-7) {
		t.Errorf("negative = %v, want -7", m["negative"])
	}
	if m["float"] != float64(3.14) {
		t.Errorf("float = %v, want 3.14", m["float"])
	}
}

func TestToJSON_NestedAnchors(t *testing.T) {
	yaml := []byte(`
nested:
  inner: &inner
    key: value
top_ref: *inner
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	topRef, ok := m["top_ref"].(map[string]any)
	if !ok {
		t.Fatalf("top_ref is not a map: %T", m["top_ref"])
	}
	if topRef["key"] != "value" {
		t.Errorf("top_ref.key = %v, want value", topRef["key"])
	}
}

func TestToJSON_Arrays(t *testing.T) {
	yaml := []byte(`
items:
  - one
  - two
  - three
`)
	j, err := ToJSON(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	items, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("items is not an array: %T", m["items"])
	}
	if len(items) != 3 {
		t.Errorf("len(items) = %d, want 3", len(items))
	}
}

func TestToJSON_InvalidYAML(t *testing.T) {
	yaml := []byte(`
invalid: [
  unclosed bracket
`)
	_, err := ToJSON(yaml)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}
