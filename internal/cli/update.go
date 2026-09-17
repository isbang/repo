package cli

import (
	"context"
	"fmt"

	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/humanize"
	"github.com/isbang/repo/internal/refresh"
	"github.com/isbang/repo/internal/semver"
	"github.com/isbang/repo/internal/update"
)

// checkForUpdate asks GitHub for the newest release, at most once per the
// configured interval.
//
// It is called from the detached background process, where the round trip
// costs nobody any wall clock. Failures are logged to the refresh log and
// otherwise ignored: not knowing about a new release is not worth an error.
func (a *app) checkForUpdate(ctx context.Context) {
	if update.Disabled() || !a.cfg.UpdateChecks() || !update.Due(a.cfg.UpdateInterval()) {
		return
	}
	st, err := update.Check(ctx, a.releaseToken(ctx))
	switch {
	case err != nil:
		refresh.Logf("update check failed: %v", err)
	case st.Latest == "":
		refresh.Logf("update check: no release published yet")
	default:
		refresh.Logf("update check: latest is %s (running %s)", st.Latest, a.versionString())
	}
}

// checkVersionNow runs the release check in the foreground, for when you want
// the answer now rather than whenever the background process next runs.
func (a *app) checkVersionNow(ctx context.Context) error {
	st, err := update.Check(ctx, a.releaseToken(ctx))
	if err != nil {
		return fmt.Errorf("update check: %w", err)
	}

	current := a.versionString()
	switch {
	case st.Latest == "":
		fmt.Printf("no release has been published on %s/%s/%s yet\n", update.Host, update.Owner, update.Name)
	case st.Newer(current):
		fmt.Println(st.NoticeFor(current))
	case !semver.Valid(current):
		fmt.Printf("this build carries no release version to compare; the latest release is %s\n", st.Latest)
	default:
		fmt.Printf("you are on the latest release (%s)\n", st.Latest)
	}
	// The answer has just been printed; don't repeat it on the way out.
	a.notifyUpdate = false
	return nil
}

// updateStatus summarises the last release check for `repo cache info`.
func (a *app) updateStatus() string {
	if update.Disabled() || !a.cfg.UpdateChecks() {
		return "off"
	}
	st := update.Load()
	current := a.versionString()
	switch {
	case st == nil:
		return "not checked yet"
	case st.Latest == "":
		return fmt.Sprintf("no release published (checked %s)", humanize.Ago(st.CheckedAt))
	case st.Newer(current):
		return fmt.Sprintf("%s available (checked %s)", st.Latest, humanize.Ago(st.CheckedAt))
	default:
		return fmt.Sprintf("up to date, latest %s (checked %s)", st.Latest, humanize.Ago(st.CheckedAt))
	}
}

// releaseToken is a token for the host releases live on. A GitHub Enterprise
// token would only be rejected there, so it is left out: the release lookup
// works unauthenticated, a token merely raises the rate limit.
func (a *app) releaseToken(ctx context.Context) string {
	if a.cfg.Host != update.Host {
		return ""
	}
	token, _, err := ghapi.Token(ctx, update.Host)
	if err != nil {
		return ""
	}
	return token
}
