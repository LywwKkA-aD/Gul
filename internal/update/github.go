package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Where the check looks, and where it sends the user.
//
// VERIFIED, and the reason this is not /releases/latest: that endpoint
// EXCLUDES prereleases, and every Gul release so far is an alpha, so it
// answers 404 on this repository. The list endpoint is ordered newest first,
// so one entry is the whole question (PLAN.md 7 M4+).
const (
	// DefaultEndpoint returns the newest release of the project, prerelease
	// or not.
	DefaultEndpoint = "https://api.github.com/repos/LywwKkA-aD/Gul/releases?per_page=1"

	// releasePagePrefix is where a tag is readable by a human. The page URL
	// is built here rather than taken from the response: html_url is a URL
	// off the network that we would be handing to the user's browser, and
	// nothing about this feature needs that trust.
	releasePagePrefix = "https://github.com/LywwKkA-aD/Gul/releases/tag/"

	// RequestTimeout bounds the whole exchange. A version check is a
	// courtesy; it may not become a reason to wait.
	RequestTimeout = 5 * time.Second

	// maxBodyBytes caps what is read. A release carries its full notes, so
	// the answer is not small, but it is not a megabyte either.
	maxBodyBytes = 1 << 20

	// userAgent identifies the client. GitHub rejects requests without one.
	userAgent = "Gul-update-check"
)

// ErrNoRelease reports that the repository has no releases at all - a fresh
// fork, or a repository whose releases were removed.
var ErrNoRelease = errors.New("update: no releases")

// Release is one release as this check reads it.
type Release struct {
	// Tag is the git tag, as GitHub spells it ("v0.4.0-alpha.1").
	Tag string `json:"tag"`
	// Version is the tag without its "v", which is how the version is
	// written everywhere else in the application.
	Version string `json:"version"`
	// URL is the release page for a human to read.
	URL string `json:"url"`
}

// Latest asks the endpoint for the newest release.
//
// The tag has to parse as a semantic version: this is the boundary where a
// value from the network becomes something the application compares and puts
// on screen, and a tag we cannot read is a tag we do not report.
func Latest(ctx context.Context, client *http.Client, endpoint string) (Release, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Release{}, fmt.Errorf("update: request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("update: fetch: %w", err)
	}
	defer func() {
		// Drain what is left so the connection can be reused, but never more
		// than the cap: the body is untrusted input either way.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		// 403 and 429 are the rate limit, 404 a repository without releases
		// (or renamed). None of them is worth a word to the user.
		return Release{}, fmt.Errorf("update: http %d", resp.StatusCode)
	}

	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&releases); err != nil {
		return Release{}, fmt.Errorf("update: decode: %w", err)
	}
	if len(releases) == 0 {
		return Release{}, ErrNoRelease
	}
	// A draft is not published: it is visible only to whoever can write to
	// the repository, and it is not something a user can install.
	if releases[0].Draft {
		return Release{}, ErrNoRelease
	}

	tag := strings.TrimSpace(releases[0].TagName)
	if _, ok := Parse(tag); !ok {
		return Release{}, fmt.Errorf("update: tag %q is not a version", tag)
	}
	return Release{
		Tag:     tag,
		Version: strings.TrimPrefix(tag, "v"),
		URL:     releasePage(tag),
	}, nil
}

// Check is the whole feature in one call: ask, compare, decide. The bool is
// false for every reason there is nothing to say - including every failure,
// which is what makes this check silent by construction.
func Check(ctx context.Context, client *http.Client, endpoint, current, dismissed string) (Release, bool, error) {
	release, err := Latest(ctx, client, endpoint)
	if err != nil {
		return Release{}, false, err
	}
	if !ShouldAnnounce(release.Tag, current, dismissed) {
		return release, false, nil
	}
	return release, true, nil
}

// releasePage is where a tag is readable. The tag has already been through
// Parse, so its characters are those of a version; escaping is belt and
// braces around a value that came off the network.
func releasePage(tag string) string {
	return releasePagePrefix + url.PathEscape(tag)
}
