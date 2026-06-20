package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// goldenRoot locates testdata/golden relative to the repo root. Tests run with
// the package dir as cwd, so we walk up to the module root.
func goldenRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		candidate := filepath.Join(dir, "testdata", "golden")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate testdata/golden")
	return ""
}

type cyrb53Case struct {
	Input string `json:"input"`
	Seed  uint32 `json:"seed"`
	Hash  string `json:"hash"`
}

func TestCyrb53Golden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(goldenRoot(t), "cyrb53.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []cyrb53Case
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cyrb53 cases")
	}
	for _, c := range cases {
		got := Cyrb53(c.Input, c.Seed)
		if got != c.Hash {
			t.Errorf("Cyrb53(%q, %d) = %q, want %q", c.Input, c.Seed, got, c.Hash)
		}
	}
}
