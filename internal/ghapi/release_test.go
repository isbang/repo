package ghapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient points a client at srv. The host is not github.com, so the
// client uses the GitHub Enterprise path prefix.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	c := New("", srv.Listener.Addr().String())
	c.HTTP = srv.Client()
	return c
}

func TestLatestRelease(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/repos/isbang/repo/releases/latest" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.0","html_url":"https://example.test/releases/v1.2.0"}`))
	})

	rel, err := c.LatestRelease(context.Background(), "isbang", "repo")
	if err != nil {
		t.Fatalf("LatestRelease() = %v", err)
	}
	if rel.TagName != "v1.2.0" || rel.HTMLURL != "https://example.test/releases/v1.2.0" {
		t.Errorf("LatestRelease() = %+v", rel)
	}
}

// A repository that only tags its versions answers 404 for the latest release.
func TestLatestReleaseFallsBackToTags(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/repos/isbang/repo/releases/latest" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		if r.URL.Path != "/api/v3/repos/isbang/repo/tags" {
			t.Errorf("path = %q", r.URL.Path)
		}
		// Unordered, with a pre-release and a tag that is not a version.
		_, _ = w.Write([]byte(`[{"name":"v0.9.0"},{"name":"latest"},{"name":"v1.3.0-rc.1"},{"name":"v1.2.0"}]`))
	})

	rel, err := c.LatestRelease(context.Background(), "isbang", "repo")
	if err != nil {
		t.Fatalf("LatestRelease() = %v", err)
	}
	if rel.TagName != "v1.2.0" {
		t.Errorf("TagName = %q, want v1.2.0", rel.TagName)
	}
	if want := "/isbang/repo/releases/tag/v1.2.0"; rel.HTMLURL == "" || rel.HTMLURL[len(rel.HTMLURL)-len(want):] != want {
		t.Errorf("HTMLURL = %q, want one ending in %q", rel.HTMLURL, want)
	}
}

func TestLatestReleaseNone(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/repos/isbang/repo/releases/latest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})

	if _, err := c.LatestRelease(context.Background(), "isbang", "repo"); !errors.Is(err, ErrNoRelease) {
		t.Errorf("LatestRelease() error = %v, want ErrNoRelease", err)
	}
}

func TestLatestReleaseAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	})

	_, err := c.LatestRelease(context.Background(), "isbang", "repo")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.Unauthorized() {
		t.Errorf("LatestRelease() error = %v, want 401 APIError", err)
	}
}
