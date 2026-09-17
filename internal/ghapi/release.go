package ghapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/isbang/repo/internal/semver"
)

// tagsPerPage is how many tags the fallback looks at when a repository
// publishes tags but no releases.
const tagsPerPage = 100

// ErrNoRelease means the repository has published neither a release nor a
// version tag yet.
var ErrNoRelease = errors.New("no release found")

// Release is the subset of a GitHub release the update check needs.
type Release struct {
	TagName     string
	HTMLURL     string
	PublishedAt time.Time
	Assets      []Asset
}

// Asset is one file published with a release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// LatestRelease returns the newest published release of owner/name, ignoring
// drafts and pre-releases as the API does.
//
// A repository that only tags its versions answers 404 here, so a missing
// release falls back to the newest version tag.
func (c *Client) LatestRelease(ctx context.Context, owner, name string) (Release, error) {
	var payload struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}

	_, err := c.getJSON(ctx, fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.baseURL(), owner, name), &payload)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return c.latestTag(ctx, owner, name)
	}
	if err != nil {
		return Release{}, err
	}
	if payload.TagName == "" {
		return Release{}, ErrNoRelease
	}
	rel := Release{
		TagName:     payload.TagName,
		HTMLURL:     payload.HTMLURL,
		PublishedAt: payload.PublishedAt,
	}
	for _, a := range payload.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return rel, nil
}

// latestTag returns the highest version tag. The tags endpoint is not ordered
// by version, so every tag is parsed and the newest one wins; tags that are not
// versions, and pre-releases, are skipped.
func (c *Client) latestTag(ctx context.Context, owner, name string) (Release, error) {
	var payload []struct {
		Name string `json:"name"`
	}
	url := fmt.Sprintf("%s/repos/%s/%s/tags?per_page=%d", c.baseURL(), owner, name, tagsPerPage)
	if _, err := c.getJSON(ctx, url, &payload); err != nil {
		return Release{}, err
	}

	var (
		best     semver.Version
		bestName string
	)
	for _, tag := range payload {
		v, ok := semver.Parse(tag.Name)
		if !ok || v.Prerelease() != "" {
			continue
		}
		if bestName == "" || v.Compare(best) > 0 {
			best, bestName = v, tag.Name
		}
	}
	if bestName == "" {
		return Release{}, ErrNoRelease
	}
	return Release{
		TagName: bestName,
		HTMLURL: fmt.Sprintf("https://%s/%s/%s/releases/tag/%s", c.webHost(), owner, name, bestName),
	}, nil
}

// webHost is the host serving the web UI, which is the API host for github.com
// but the same host on GitHub Enterprise.
func (c *Client) webHost() string {
	if c.host == "api.github.com" {
		return "github.com"
	}
	return c.host
}
