package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isbang/repo/internal/ghapi"
)

// useTempState points the state file at a temporary file for the test.
func useTempState(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update.json")
	t.Setenv("REPO_UPDATE_FILE", path)
	return path
}

func TestLoadMissing(t *testing.T) {
	useTempState(t)
	if s := Load(); s != nil {
		t.Errorf("Load() = %+v, want nil", s)
	}
}

func TestLoadIgnoresGarbage(t *testing.T) {
	path := useTempState(t)
	for _, content := range []string{"", "{not json", `{"version":0,"latest":"v9.9.9"}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if s := Load(); s != nil {
			t.Errorf("Load() with %q = %+v, want nil", content, s)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := useTempState(t)
	want := &State{CheckedAt: time.Now().Truncate(time.Second), Latest: "v1.2.0", URL: "https://example.test/v1.2.0"}
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

	got := Load()
	if got == nil {
		t.Fatal("Load() = nil")
	}
	if got.Latest != want.Latest || got.URL != want.URL || !got.CheckedAt.Equal(want.CheckedAt) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestDue(t *testing.T) {
	useTempState(t)
	if !Due(DefaultInterval) {
		t.Error("Due() on a missing state = false, want true")
	}

	if err := Save(&State{CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if Due(DefaultInterval) {
		t.Error("Due() right after a check = true, want false")
	}
	if !Due(0) {
		t.Error("Due(0) = false, want true")
	}

	if err := Save(&State{CheckedAt: time.Now().Add(-25 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if !Due(DefaultInterval) {
		t.Error("Due() a day later = false, want true")
	}
}

func TestNoticeFor(t *testing.T) {
	tests := []struct {
		name    string
		state   *State
		current string
		want    bool
	}{
		{"newer release", &State{Latest: "v1.2.0"}, "v1.1.0", true},
		{"same version", &State{Latest: "v1.2.0"}, "v1.2.0", false},
		{"older release", &State{Latest: "v1.0.0"}, "v1.1.0", false},
		{"pre-release of what we run", &State{Latest: "v1.2.0-rc.1"}, "v1.2.0", false},
		{"development build", &State{Latest: "v1.2.0"}, "dev", false},
		{"nothing published", &State{}, "v1.1.0", false},
		{"never checked", nil, "v1.1.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.state.NoticeFor(tt.current)
			if (got != "") != tt.want {
				t.Fatalf("NoticeFor(%q) = %q, want notice=%v", tt.current, got, tt.want)
			}
			if got == "" {
				return
			}
			if !strings.Contains(got, tt.state.Latest) || !strings.Contains(got, tt.current) {
				t.Errorf("NoticeFor(%q) = %q, want it to name both versions", tt.current, got)
			}
			if !strings.Contains(got, ReleasesURL) {
				t.Errorf("NoticeFor(%q) = %q, want the releases URL", tt.current, got)
			}
		})
	}
}

func TestNoticeDisabled(t *testing.T) {
	useTempState(t)
	if err := Save(&State{CheckedAt: time.Now(), Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	if Notice("v1.0.0") == "" {
		t.Fatal("Notice() = \"\", want a notice before disabling")
	}

	t.Setenv(EnvDisable, "1")
	if got := Notice("v1.0.0"); got != "" {
		t.Errorf("Notice() with %s set = %q, want \"\"", EnvDisable, got)
	}
}

// testClient points the check at a test server instead of github.com.
func testClient(t *testing.T, handler http.HandlerFunc) *ghapi.Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	c := ghapi.New("", srv.Listener.Addr().String())
	c.HTTP = srv.Client()
	return c
}

func TestCheckRecordsRelease(t *testing.T) {
	useTempState(t)
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2.0.0","html_url":"https://example.test/v2.0.0"}`))
	})

	got, err := check(context.Background(), client)
	if err != nil {
		t.Fatalf("check() = %v", err)
	}
	if got.Latest != "v2.0.0" {
		t.Errorf("Latest = %q, want v2.0.0", got.Latest)
	}
	if saved := Load(); saved == nil || saved.Latest != "v2.0.0" {
		t.Errorf("Load() = %+v, want the checked release", saved)
	}
	if Due(DefaultInterval) {
		t.Error("Due() right after a check = true, want false")
	}
}

// An unreleased repository still records the check, so it is asked about once
// per interval rather than once per command.
func TestCheckRecordsAbsentRelease(t *testing.T) {
	useTempState(t)
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})

	got, err := check(context.Background(), client)
	if err != nil {
		t.Fatalf("check() = %v", err)
	}
	if got.Latest != "" {
		t.Errorf("Latest = %q, want \"\"", got.Latest)
	}
	if Due(DefaultInterval) {
		t.Error("Due() right after a check = true, want false")
	}
	if got.NoticeFor("v1.0.0") != "" {
		t.Error("NoticeFor() with nothing published, want no notice")
	}
}

func TestCheckFailureLeavesStateAlone(t *testing.T) {
	useTempState(t)
	previous := &State{CheckedAt: time.Now().Add(-48 * time.Hour), Latest: "v1.0.0"}
	if err := Save(previous); err != nil {
		t.Fatal(err)
	}

	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := check(context.Background(), client); err == nil {
		t.Fatal("check() = nil, want an error")
	}
	if saved := Load(); saved == nil || saved.Latest != "v1.0.0" {
		t.Errorf("Load() = %+v, want the previous state untouched", saved)
	}
}
