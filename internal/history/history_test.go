package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func useTempHistory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.json")
	t.Setenv("REPO_HISTORY_FILE", path)
	return path
}

func write(t *testing.T, path string, f File) {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	useTempHistory(t)
	if got := Load(); len(got.Events) != 0 {
		t.Errorf("Load() = %+v, want empty", got)
	}
}

func TestLoadIgnoresCorruptAndOldVersions(t *testing.T) {
	path := useTempHistory(t)
	for _, content := range []string{"not json", `{"version":0,"events":[{"full_name":"a/b"}]}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := Load(); len(got.Events) != 0 {
			t.Errorf("Load() with %q = %+v, want empty", content, got)
		}
	}
}

func TestRecordAndCount(t *testing.T) {
	path := useTempHistory(t)

	for range 3 {
		if err := Record("isbang/repo"); err != nil {
			t.Fatalf("Record() = %v", err)
		}
	}
	if err := Record("acme/legacy"); err != nil {
		t.Fatal(err)
	}

	counts := Counts()
	if counts["isbang/repo"] != 3 {
		t.Errorf("isbang/repo = %d, want 3", counts["isbang/repo"])
	}
	if counts["acme/legacy"] != 1 {
		t.Errorf("acme/legacy = %d, want 1", counts["acme/legacy"])
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}
}

func TestRecordEmptyNameIsNoop(t *testing.T) {
	path := useTempHistory(t)
	if err := Record(""); err != nil {
		t.Fatalf("Record(\"\") = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Record(\"\") wrote a file")
	}
}

func TestCountsIgnoreEventsOutsideTheWindow(t *testing.T) {
	path := useTempHistory(t)
	now := time.Now()
	write(t, path, File{Version: Version, Events: []Event{
		{FullName: "old/one", At: now.Add(-CountWindow - time.Hour)},
		{FullName: "new/one", At: now.Add(-time.Hour)},
	}})

	counts := Counts()
	if _, ok := counts["old/one"]; ok {
		t.Error("an event older than the window was counted")
	}
	if counts["new/one"] != 1 {
		t.Errorf("new/one = %d, want 1", counts["new/one"])
	}
}

func TestCountsUseOnlyTheMostRecentEvents(t *testing.T) {
	path := useTempHistory(t)
	now := time.Now()

	// CountEvents recent clones of "new/one" push "old/one" out of the window,
	// even though it is inside the time window.
	events := []Event{{FullName: "old/one", At: now.Add(-24 * time.Hour)}}
	for i := range CountEvents {
		events = append(events, Event{FullName: "new/one", At: now.Add(-time.Duration(CountEvents-i) * time.Minute)})
	}
	write(t, path, File{Version: Version, Events: events})

	counts := Counts()
	if counts["new/one"] != CountEvents {
		t.Errorf("new/one = %d, want %d", counts["new/one"], CountEvents)
	}
	if _, ok := counts["old/one"]; ok {
		t.Errorf("old/one counted, but only the most recent %d events count", CountEvents)
	}
}

func TestRecordPrunesOldEventsAndCaps(t *testing.T) {
	path := useTempHistory(t)
	now := time.Now()

	events := []Event{{FullName: "ancient/one", At: now.Add(-retention - time.Hour)}}
	for i := range maxEvents + 10 {
		events = append(events, Event{FullName: "recent/one", At: now.Add(-time.Duration(i) * time.Second)})
	}
	write(t, path, File{Version: Version, Events: events})

	if err := Record("isbang/repo"); err != nil {
		t.Fatal(err)
	}

	f := Load()
	if len(f.Events) > maxEvents {
		t.Errorf("events = %d, want at most %d", len(f.Events), maxEvents)
	}
	for _, e := range f.Events {
		if e.FullName == "ancient/one" {
			t.Error("an event older than the retention window survived")
		}
	}
	if last := f.Events[len(f.Events)-1]; last.FullName != "isbang/repo" {
		t.Errorf("last event = %q, want the one just recorded", last.FullName)
	}
}

func TestRecordIsSafeFromSeveralWorkers(t *testing.T) {
	useTempHistory(t)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Record("isbang/repo"); err != nil {
				t.Errorf("Record() = %v", err)
			}
		}(i)
	}
	wg.Wait()

	if got := Counts()["isbang/repo"]; got != 20 {
		t.Errorf("count = %d, want 20 (no lost writes)", got)
	}
}

func TestRemove(t *testing.T) {
	useTempHistory(t)
	if removed, err := Remove(); err != nil || removed {
		t.Errorf("Remove() on missing file = %v, %v", removed, err)
	}
	if err := Record("isbang/repo"); err != nil {
		t.Fatal(err)
	}
	if removed, err := Remove(); err != nil || !removed {
		t.Errorf("Remove() = %v, %v", removed, err)
	}
}
