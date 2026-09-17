package selfupdate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/isbang/repo/internal/ghapi"
)

// TestApplyRealArchive drives an archive built by `make dist` through the
// upgrade path, so the release layout and the installer stay in step. It skips
// when no archive has been built.
func TestApplyRealArchive(t *testing.T) {
	dist, err := filepath.Abs("../../dist")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dist, fmt.Sprintf("repo_*_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Skip("run `make dist` first")
	}
	name := filepath.Base(matches[0])

	srv := httptest.NewServer(http.FileServer(http.Dir(dist)))
	defer srv.Close()

	rel := ghapi.Release{
		TagName: strings.TrimSuffix(name, ".tar.gz"),
		Assets: []ghapi.Asset{
			{Name: name, URL: srv.URL + "/" + name},
			{Name: ChecksumsName, URL: srv.URL + "/" + ChecksumsName},
		},
	}

	target := filepath.Join(t.TempDir(), "repo")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), rel, target, nil); err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	out, err := exec.Command(target, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("running the installed binary: %v (%s)", err, out)
	}
	if !strings.HasPrefix(string(out), "repo ") {
		t.Errorf("installed binary reports %q, want a version line", strings.TrimSpace(string(out)))
	}
	t.Logf("installed binary says: %s", strings.TrimSpace(string(out)))
}
