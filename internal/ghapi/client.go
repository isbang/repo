// Package ghapi is a small GitHub REST client: just enough to list every
// repository the authenticated user can reach.
package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiVersion  = "2022-11-28"
	perPage     = 100
	maxPageJobs = 6 // concurrent page fetches
	maxAttempts = 3
)

// Repo is the subset of a GitHub repository this tool needs.
type Repo struct {
	FullName      string    `json:"full_name"`
	Name          string    `json:"name"`
	Owner         string    `json:"owner"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	Archived      bool      `json:"archived"`
	Stars         int       `json:"stars"`
	Language      string    `json:"language"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	CloneURL      string    `json:"clone_url"`
	SSHURL        string    `json:"ssh_url"`
	HTMLURL       string    `json:"html_url"`
}

// apiRepo mirrors the API payload; Repo is the flattened form we cache.
type apiRepo struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	Archived      bool      `json:"archived"`
	Stars         int       `json:"stargazers_count"`
	Language      string    `json:"language"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	CloneURL      string    `json:"clone_url"`
	SSHURL        string    `json:"ssh_url"`
	HTMLURL       string    `json:"html_url"`
}

func (a apiRepo) toRepo() Repo {
	return Repo{
		FullName:      a.FullName,
		Name:          a.Name,
		Owner:         a.Owner.Login,
		Description:   a.Description,
		Private:       a.Private,
		Fork:          a.Fork,
		Archived:      a.Archived,
		Stars:         a.Stars,
		Language:      a.Language,
		DefaultBranch: a.DefaultBranch,
		PushedAt:      a.PushedAt,
		CloneURL:      a.CloneURL,
		SSHURL:        a.SSHURL,
		HTMLURL:       a.HTMLURL,
	}
}

// CloneURLFor returns the URL to clone from for the given protocol.
func (r Repo) CloneURLFor(protocol string) string {
	if protocol == "ssh" && r.SSHURL != "" {
		return r.SSHURL
	}
	return r.CloneURL
}

// APIError is a non-2xx response from the GitHub API.
type APIError struct {
	Status  int
	Message string
	URL     string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("GitHub API %s: %d", e.URL, e.Status)
	}
	return fmt.Sprintf("GitHub API %s: %d: %s", e.URL, e.Status, e.Message)
}

// Unauthorized reports whether the token was rejected.
func (e *APIError) Unauthorized() bool { return e.Status == http.StatusUnauthorized }

// Client talks to one GitHub host.
type Client struct {
	HTTP      *http.Client
	UserAgent string

	token string
	host  string
}

// New returns a client for host authenticated with token.
func New(token, host string) *Client {
	if host == "" {
		host = "github.com"
	}
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: "isbang-repo-cli",
		token:     token,
		host:      host,
	}
}

// baseURL is the REST root for the client's host.
func (c *Client) baseURL() string {
	if c.host == "github.com" || c.host == "api.github.com" {
		return "https://api.github.com"
	}
	return "https://" + c.host + "/api/v3"
}

// Viewer returns the login of the authenticated user.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if _, err := c.getJSON(ctx, c.baseURL()+"/user", &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

// ListRepos returns every repository the authenticated user can reach.
//
// Page 1 is fetched first to learn the page count from the Link header, then
// the remaining pages are fetched concurrently. progress, if non-nil, is called
// with the number of pages done and the total known so far.
func (c *Client) ListRepos(ctx context.Context, affiliation string, progress func(done, total int)) ([]Repo, error) {
	if progress == nil {
		progress = func(int, int) {}
	}

	first, links, err := c.reposPage(ctx, affiliation, 1)
	if err != nil {
		return nil, err
	}
	last := lastPage(links)
	progress(1, last)

	repos := make([]Repo, 0, len(first)*max(last, 1))
	repos = append(repos, first...)

	if last <= 1 {
		return dedupe(repos), nil
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		done     = 1
		sem      = make(chan struct{}, maxPageJobs)
	)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for page := 2; page <= last; page++ {
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			got, _, err := c.reposPage(ctx, affiliation, page)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel() // stop the remaining pages
				}
				return
			}
			repos = append(repos, got...)
			done++
			progress(done, last)
		}(page)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return dedupe(repos), nil
}

func (c *Client) reposPage(ctx context.Context, affiliation string, page int) ([]Repo, string, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("page", strconv.Itoa(page))
	q.Set("sort", "full_name")
	if affiliation != "" {
		q.Set("affiliation", affiliation)
	}

	var payload []apiRepo
	links, err := c.getJSON(ctx, c.baseURL()+"/user/repos?"+q.Encode(), &payload)
	if err != nil {
		return nil, "", err
	}
	out := make([]Repo, 0, len(payload))
	for _, r := range payload {
		out = append(out, r.toRepo())
	}
	return out, links, nil
}

// getJSON performs a GET and decodes the body into v, returning the Link header.
func (c *Client) getJSON(ctx context.Context, rawURL string, v any) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", apiVersion)
		req.Header.Set("User-Agent", c.UserAgent)
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			lastErr = err
			if !sleep(ctx, backoff(attempt)) {
				return "", ctx.Err()
			}
			continue
		}

		if resp.StatusCode/100 == 2 {
			defer resp.Body.Close()
			if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
				return "", fmt.Errorf("decode %s: %w", rawURL, err)
			}
			return resp.Header.Get("Link"), nil
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		apiErr := &APIError{Status: resp.StatusCode, Message: apiMessage(body), URL: shortURL(rawURL)}

		if retryable(resp.StatusCode) && attempt < maxAttempts {
			lastErr = apiErr
			if !sleep(ctx, retryAfter(resp.Header, attempt)) {
				return "", ctx.Err()
			}
			continue
		}
		if resp.StatusCode == http.StatusForbidden && strings.Contains(apiErr.Message, "rate limit") {
			apiErr.Message += " (wait for the rate limit to reset)"
		}
		return "", apiErr
	}
	return "", lastErr
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func backoff(attempt int) time.Duration {
	return time.Duration(1<<(attempt-1)) * time.Second
}

func retryAfter(h http.Header, attempt int) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 60 {
			return time.Duration(n) * time.Second
		}
	}
	return backoff(attempt)
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func apiMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Message != "" {
		return payload.Message
	}
	return strings.TrimSpace(string(body))
}

// shortURL drops the query string so errors stay readable.
func shortURL(raw string) string {
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		return raw[:i]
	}
	return raw
}

var lastPageRe = regexp.MustCompile(`[?&]page=(\d+)[^>]*>;\s*rel="last"`)

// lastPage extracts the rel="last" page number from a Link header. It returns 1
// when the header has no such link (single page of results).
func lastPage(link string) int {
	m := lastPageRe.FindStringSubmatch(link)
	if m == nil {
		return 1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// dedupe sorts by full name and drops duplicates, which can appear when a page
// boundary shifts between requests.
func dedupe(repos []Repo) []Repo {
	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].FullName) < strings.ToLower(repos[j].FullName)
	})
	out := repos[:0]
	seen := ""
	for _, r := range repos {
		if r.FullName == seen {
			continue
		}
		seen = r.FullName
		out = append(out, r)
	}
	return out
}

// IsNoToken reports whether err is the "no token" sentinel.
func IsNoToken(err error) bool { return errors.Is(err, ErrNoToken) }
