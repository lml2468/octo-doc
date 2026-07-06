package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// releaseRepo is the GitHub repo whose Releases host the octo binaries.
const releaseRepo = "Mininglamp-OSS/octo-doc"

// cmdUpdate self-updates the octo binary from the latest GitHub Release. With
// --check it only reports the current-vs-latest versions without downloading.
//
//	octo update [--check]
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	check := fs.Bool("check", false, "report the latest version without installing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rel, err := latestRelease(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("current: %s\nlatest:  %s\n", version, rel.Tag)
	if rel.Tag == version {
		fmt.Println("already up to date.")
		return nil
	}
	if *check {
		fmt.Println("run `octo update` to install.")
		return nil
	}

	// Asset naming: octo_<os>_<arch>[.exe]. Checksums live in SHA256SUMS.
	assetName := fmt.Sprintf("octo_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}
	assetURL := rel.assetURL(assetName)
	sumsURL := rel.assetURL("SHA256SUMS")
	if assetURL == "" {
		return fmt.Errorf("no release asset %q for %s (available: %s)", assetName, rel.Tag, rel.assetNames())
	}

	fmt.Printf("downloading %s...\n", assetName)
	bin, err := download(ctx, assetURL)
	if err != nil {
		return err
	}
	if sumsURL != "" {
		if err := verifyChecksum(ctx, sumsURL, assetName, bin); err != nil {
			return err
		}
		fmt.Println("checksum verified.")
	}
	if err := replaceSelf(bin); err != nil {
		return err
	}
	fmt.Printf("updated to %s.\n", rel.Tag)
	return nil
}

// ghRelease is the subset of the GitHub Releases API response we consume.
type ghRelease struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r ghRelease) assetURL(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func (r ghRelease) assetNames() string {
	names := make([]string, 0, len(r.Assets))
	for _, a := range r.Assets {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}

// latestRelease fetches the latest published release metadata.
func latestRelease(ctx context.Context) (*ghRelease, error) {
	url := "https://api.github.com/repos/" + releaseRepo + "/releases/latest"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub releases API: HTTP %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.Tag == "" {
		return nil, fmt.Errorf("no published release found")
	}
	return &rel, nil
}

// download fetches a URL's full body into memory.
func download(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// verifyChecksum confirms bin's sha256 matches the SHA256SUMS entry for assetName.
func verifyChecksum(ctx context.Context, sumsURL, assetName string, bin []byte) error {
	sums, err := download(ctx, sumsURL)
	if err != nil {
		return err
	}
	want := ""
	for line := range strings.SplitSeq(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && (fields[1] == assetName || fields[1] == "*"+assetName) {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum for %s in SHA256SUMS", assetName)
	}
	sum := sha256.Sum256(bin)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, want)
	}
	return nil
}

// replaceSelf atomically replaces the running executable with new bytes. It
// writes a sibling temp file (same dir, so the rename stays on one filesystem),
// chmods it executable, and renames over the current binary.
func replaceSelf(newBin []byte) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}
	dir := filepath.Dir(self)
	tmp, err := os.CreateTemp(dir, ".octo-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (need write permission there): %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(newBin); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, self)
}
