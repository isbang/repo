package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/isbang/repo/internal/ghapi"
)

func TestFind(t *testing.T) {
	rel := ghapi.Release{
		TagName: "v1.2.0",
		Assets: []ghapi.Asset{
			{Name: ChecksumsName, URL: "https://example.test/checksums.txt"},
			{Name: "repo_v1.2.0_darwin_arm64.tar.gz", URL: "https://example.test/darwin"},
			{Name: "repo_v1.2.0_linux_amd64.tar.gz", URL: "https://example.test/linux"},
		},
	}

	got, err := Find(rel, "linux", "amd64")
	if err != nil {
		t.Fatalf("Find() = %v", err)
	}
	if got.URL != "https://example.test/linux" {
		t.Errorf("Find() = %+v, want the linux/amd64 asset", got)
	}

	// linux/arm64 is not published: no guessing, no "close enough" match.
	if _, err := Find(rel, "linux", "arm64"); !errors.Is(err, ErrNoAsset) {
		t.Errorf("Find(linux/arm64) error = %v, want ErrNoAsset", err)
	}
}

func TestParseChecksums(t *testing.T) {
	sums := ParseChecksums(`
ABCDEF  repo_v1.2.0_linux_amd64.tar.gz
123456 *repo_v1.2.0_windows_amd64.tar.gz
garbage
`)
	if got := sums["repo_v1.2.0_linux_amd64.tar.gz"]; got != "abcdef" {
		t.Errorf("linux sum = %q, want abcdef", got)
	}
	if got := sums["repo_v1.2.0_windows_amd64.tar.gz"]; got != "123456" {
		t.Errorf("windows sum = %q, want 123456", got)
	}
	if len(sums) != 2 {
		t.Errorf("parsed %d entries, want 2", len(sums))
	}
}

// tarGz builds a .tar.gz holding one file, the way a release archive does.
func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// release serves an archive and its checksums, and returns the release that
// points at them.
func release(t *testing.T, archive []byte, sums string) ghapi.Release {
	t.Helper()
	assetName := fmt.Sprintf("repo_v1.2.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+ChecksumsName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sums))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return ghapi.Release{
		TagName: "v1.2.0",
		Assets: []ghapi.Asset{
			{Name: assetName, URL: srv.URL + "/" + assetName, Size: int64(len(archive))},
			{Name: ChecksumsName, URL: srv.URL + "/" + ChecksumsName},
		},
	}
}

// installed writes a stand-in for the binary being replaced.
func installed(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repo")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApply(t *testing.T) {
	archive := tarGz(t, "repo", "new binary")
	rel := release(t, archive, fmt.Sprintf("%s  %s\n", sha256Hex(archive), rel1Name()))
	target := installed(t, "old binary")

	var lastDone, lastTotal int64
	if err := Apply(context.Background(), rel, target, func(done, total int64) {
		lastDone, lastTotal = done, total
	}); err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary" {
		t.Errorf("target content = %q, want the downloaded binary", got)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o755 {
		t.Errorf("mode = %v, want 0755", perm)
	}
	if lastDone != int64(len(archive)) || lastTotal != int64(len(archive)) {
		t.Errorf("progress = %d/%d, want %d/%d", lastDone, lastTotal, len(archive), len(archive))
	}
	// Nothing is left behind in the install directory.
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("install dir holds %d files, want just the binary", len(entries))
	}
}

// A tampered download must never reach the install path.
func TestApplyChecksumMismatch(t *testing.T) {
	archive := tarGz(t, "repo", "tampered binary")
	rel := release(t, archive, fmt.Sprintf("%s  %s\n", sha256Hex([]byte("something else")), rel1Name()))
	target := installed(t, "old binary")

	if err := Apply(context.Background(), rel, target, nil); err == nil {
		t.Fatal("Apply() = nil, want a checksum error")
	}
	if got, _ := os.ReadFile(target); string(got) != "old binary" {
		t.Errorf("target content = %q, want it untouched", got)
	}
}

func TestApplyWithoutChecksums(t *testing.T) {
	archive := tarGz(t, "repo", "new binary")
	rel := release(t, archive, "")
	rel.Assets = rel.Assets[:1] // drop checksums.txt
	target := installed(t, "old binary")

	err := Apply(context.Background(), rel, target, nil)
	if !errors.Is(err, ErrNoChecksum) {
		t.Fatalf("Apply() = %v, want ErrNoChecksum", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "old binary" {
		t.Errorf("target content = %q, want it untouched", got)
	}
}

func TestApplyNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "repo")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	archive := tarGz(t, "repo", "new binary")
	rel := release(t, archive, fmt.Sprintf("%s  %s\n", sha256Hex(archive), rel1Name()))

	if err := Apply(context.Background(), rel, target, nil); !errors.Is(err, ErrNotWritable) {
		t.Errorf("Apply() = %v, want ErrNotWritable", err)
	}
}

func rel1Name() string {
	return fmt.Sprintf("repo_v1.2.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}
