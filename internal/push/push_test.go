package push

import (
	"strings"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	c := &Client{baseVersion: "v1"}

	prov := Provenance{
		Repository: "org/my-repo",
		Branch:     "main",
		FilePath:   "charts/accounts.chart.yaml",
		CommitSHA:  "abc1234567890def1234567890abcdef12345678",
	}

	rawYAML := []byte("apiVersion: formance.com/v1alpha1\nkind: Chart\n")
	version := c.BuildVersion(rawYAML, prov)

	// Should start with base version + build metadata separator
	if !strings.HasPrefix(version, "v1+") {
		t.Errorf("version should start with 'v1+', got: %s", version)
	}

	// Should contain sanitized repo name (/ becomes -)
	if !strings.Contains(version, "org-my-repo") {
		t.Errorf("version should contain sanitized repo name, got: %s", version)
	}

	// Should contain branch
	if !strings.Contains(version, ".main.") {
		t.Errorf("version should contain branch name, got: %s", version)
	}

	// Should contain truncated commit SHA (7 chars)
	if !strings.Contains(version, ".abc1234.") {
		t.Errorf("version should contain 7-char commit SHA, got: %s", version)
	}

	// Should contain 8-char file hash
	parts := strings.Split(version, ".")
	lastPart := parts[len(parts)-1]
	if len(lastPart) != 8 {
		t.Errorf("last segment (file hash) should be 8 chars, got: %q (%d chars)", lastPart, len(lastPart))
	}

	// Should be deterministic
	version2 := c.BuildVersion(rawYAML, prov)
	if version != version2 {
		t.Errorf("BuildVersion should be deterministic: %s != %s", version, version2)
	}

	// Different YAML content should produce different hash
	version3 := c.BuildVersion([]byte("different content"), prov)
	if version == version3 {
		t.Error("different YAML content should produce different version")
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int // -1, 0, 1
	}{
		{"v2.4.0", "v2.4.0", 0},
		{"v2.4.0", "v2.3.0", 1},
		{"v2.3.0", "v2.4.0", -1},
		{"v2.4.1", "v2.4.0", 1},
		{"v2.4.0", "v2.4.0-rc.2", 1},     // release > pre-release
		{"v2.4.0-rc.2", "v2.4.0", -1},     // pre-release < release
		{"v2.4.0-rc.2", "v2.4.0-rc.2", 0}, // equal pre-release
		{"v2.4.0-rc.2", "v2.4.0-beta.1", 1},  // rc > beta (lexicographic)
		{"v2.4.0-beta.1", "v2.4.0-rc.2", -1},
		{"v2.5.0-beta.1", "v2.4.0-rc.2", 1},  // higher minor wins
		{"v3.0.0", "v2.4.0-rc.2", 1},
		{"2.4.0", "v2.4.0", 0},            // with/without v prefix
	}

	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSupportsSchemas(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"v2.4.0-rc.2", true},
		{"v2.4.0-rc.3", true},
		{"v2.4.0", true},
		{"v2.5.0", true},
		{"v3.0.0", true},
		{"v2.4.0-beta.1", false},
		{"v2.4.0-beta.3", false},
		{"v2.3.13", false},
		{"v2.3.0", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		vi := &VersionInfo{
			Components: []ComponentVersion{{Name: "ledger", Version: tt.version}},
		}
		got, _ := vi.SupportsSchemas()
		if got != tt.want {
			t.Errorf("SupportsSchemas() with ledger %q = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestFileHash(t *testing.T) {
	yaml1 := []byte("apiVersion: formance.com/v1alpha1\nkind: Chart\n")
	yaml2 := []byte("different content")

	h1 := FileHash(yaml1)
	h2 := FileHash(yaml2)

	if len(h1) != 8 {
		t.Errorf("FileHash should return 8 chars, got %d: %q", len(h1), h1)
	}
	if h1 == h2 {
		t.Error("different content should produce different hashes")
	}
	if h1 != FileHash(yaml1) {
		t.Error("FileHash should be deterministic")
	}
}

func TestFileHashFromVersion(t *testing.T) {
	tests := []struct {
		version string
		want    string
		wantOK  bool
	}{
		{
			"v1.0.12+thierrycoopman-formance-charts.main.charts-analog.fbo.schema.yaml.09f5caf.e57b43ad",
			"e57b43ad",
			true,
		},
		{
			"v1+org-my-repo.main.charts-accounts.chart.yaml.abc1234.f3e9a0b1",
			"f3e9a0b1",
			true,
		},
		{"v1", "", false},
		{"v1+", "", false},
		{"v1+single", "", false},
	}

	for _, tt := range tests {
		got, ok := FileHashFromVersion(tt.version)
		if ok != tt.wantOK {
			t.Errorf("FileHashFromVersion(%q): ok=%v, want %v", tt.version, ok, tt.wantOK)
		}
		if got != tt.want {
			t.Errorf("FileHashFromVersion(%q) = %q, want %q", tt.version, got, tt.want)
		}
	}
}

func TestCollectFileHashes(t *testing.T) {
	schemas := []Schema{
		{Version: "v1+org-repo.main.charts-foo.yaml.abc1234.e57b43ad"},
		{Version: "v1+org-repo.main.charts-bar.yaml.def5678.f3e9a0b1"},
		{Version: "v1+org-repo.main.charts-baz.yaml.ghi9012.e57b43ad"}, // duplicate hash
		{Version: "v1"},                                                   // no metadata
	}

	hashes := CollectFileHashes(schemas)

	if len(hashes) != 2 {
		t.Errorf("expected 2 unique hashes, got %d: %v", len(hashes), hashes)
	}
	if !hashes["e57b43ad"] {
		t.Error("missing hash e57b43ad")
	}
	if !hashes["f3e9a0b1"] {
		t.Error("missing hash f3e9a0b1")
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"org/repo", "org-repo"},
		{"feature/my-branch", "feature-my-branch"},
		{"path/to/file.yaml", "path-to-file.yaml"},
		{"name with spaces", "name-with-spaces"},
	}

	for _, tt := range tests {
		got := sanitize(tt.input)
		if got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
