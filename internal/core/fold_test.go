package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type foldIn struct {
	List    []Comment       `json:"list"`
	Version json.RawMessage `json:"version"`
}

// parseVersion turns the golden's version field ("all" | number) into an int.
func parseVersion(raw json.RawMessage) int {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s == "all" {
		return VersionLatest
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	return VersionLatest
}

func readGolden(t *testing.T, parts ...string) []byte {
	t.Helper()
	p := filepath.Join(append([]string{goldenRoot(t)}, parts...)...)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// assertJSONEqual compares two JSON documents structurally (key order and
// whitespace independent), reporting a readable diff on mismatch.
func assertJSONEqual(t *testing.T, got any, wantRaw []byte, label string) {
	t.Helper()
	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotN, wantN any
	if err := json.Unmarshal(gotRaw, &gotN); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantRaw, &wantN); err != nil {
		t.Fatal(err)
	}
	gotC, _ := json.MarshalIndent(gotN, "", " ")
	wantC, _ := json.MarshalIndent(wantN, "", " ")
	if string(gotC) != string(wantC) {
		t.Errorf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", label, gotC, wantC)
	}
}

func TestFoldGolden(t *testing.T) {
	dir := filepath.Join(goldenRoot(t), "fold")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" || len(name) < len(".in.json") {
			continue
		}
		base, ok := stripSuffix(name, ".in.json")
		if !ok {
			continue
		}
		t.Run(base, func(t *testing.T) {
			var in foldIn
			if err := json.Unmarshal(readGolden(t, "fold", base+".in.json"), &in); err != nil {
				t.Fatal(err)
			}
			v := parseVersion(in.Version)
			var got []CommentSnapshot
			if v == VersionLatest {
				got = HistoryList(in.List)
			} else {
				got = SnapshotList(in.List, v)
			}
			assertJSONEqual(t, got, readGolden(t, "fold", base+".out.json"), base)
		})
	}
}

func stripSuffix(s, suffix string) (string, bool) {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)], true
	}
	return "", false
}
