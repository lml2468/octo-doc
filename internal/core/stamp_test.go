package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStampGolden(t *testing.T) {
	dir := filepath.Join(goldenRoot(t), "stamp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		base, ok := stripSuffix(e.Name(), ".in.html")
		if !ok {
			continue
		}
		t.Run(base, func(t *testing.T) {
			in := readGolden(t, "stamp", base+".in.html")
			wantHTML := readGolden(t, "stamp", base+".out.html")
			res := StampAids(string(in))

			// BYTE-equivalence on the stamped HTML — the core contract.
			if res.HTML != string(wantHTML) {
				t.Errorf("HTML byte mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s",
					base, res.HTML, wantHTML)
			}

			// aids index parity.
			var wantAIDs []StampedArtifact
			if err := json.Unmarshal(readGolden(t, "stamp", base+".aids.json"), &wantAIDs); err != nil {
				t.Fatal(err)
			}
			// normalize nil vs empty slice for comparison
			got := res.AIDs
			if got == nil {
				got = []StampedArtifact{}
			}
			if wantAIDs == nil {
				wantAIDs = []StampedArtifact{}
			}
			assertJSONEqual(t, got, mustJSON(t, wantAIDs), base+" aids")
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
