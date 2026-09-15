package cache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/isbang/repo/internal/ghapi"
)

// useTempCache points the cache at a temporary file for the test.
func useTempCache(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repos.json")
	t.Setenv("REPO_CACHE_FILE", path)
	return path
}

func TestLoadMissing(t *testing.T) {
	useTempCache(t)
	if _, err := Load(); !errors.Is(err, ErrNotExist) {
		t.Errorf("Load() error = %v, want ErrNotExist", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := useTempCache(t)
	want := &File{
		Host:      "github.com",
		User:      "isbang",
		FetchedAt: time.Now().Truncate(time.Second),
		Repos:     []ghapi.Repo{{FullName: "isbang/repo", Name: "repo", Owner: "isbang"}},
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got.Version != Version || got.User != want.User || len(got.Repos) != 1 {
		t.Fatalf("Load() = %+v", got)
	}
	if !got.FetchedAt.Equal(want.FetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, want.FetchedAt)
	}
}

func TestLoadRejectsCorruptAndOldVersions(t *testing.T) {
	path := useTempCache(t)
	for _, content := range []string{"", "not json", `{"version":0,"repos":[]}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(); !errors.Is(err, ErrNotExist) {
			t.Errorf("Load() with %q error = %v, want ErrNotExist", content, err)
		}
	}
}

func TestRemove(t *testing.T) {
	useTempCache(t)
	if removed, err := Remove(); err != nil || removed {
		t.Errorf("Remove() on missing file = %v, %v", removed, err)
	}
	if err := Save(&File{}); err != nil {
		t.Fatal(err)
	}
	if removed, err := Remove(); err != nil || !removed {
		t.Errorf("Remove() = %v, %v", removed, err)
	}
}
