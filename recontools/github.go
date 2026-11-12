package recontools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

// allowedHosts mirrors selfupdate's allowlist. Kept as a separate copy, not a
// shared export, because this package's trust boundary is different: it fetches
// from arbitrary (if cataloged) third-party GitHub orgs, not Counterweight's own repo.
var allowedHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
	"raw.githubusercontent.com":            true,
}

// Release holds the GitHub release fields this installer needs.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset describes one attached release file.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// NewClient accepts only GitHub hosts, matching selfupdate's client policy.
func NewClient(proxy string) *http.Client {
	tr := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if p := strings.TrimSpace(proxy); p != "" {
		if pu, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	return &http.Client{
		Transport: tr,
		Timeout:   10 * time.Minute, // These binaries are a few MB; generous but bounded.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return locale.NewError("Too many redirects")
			}
			return checkURL(req.URL)
		},
	}
}

func checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return locale.Errorf("Non-HTTPS URL refused: %s", u.Scheme+"://"+u.Host)
	}
	if !allowedHosts[strings.ToLower(u.Hostname())] {
		return locale.Errorf("Non-GitHub host refused: %s", u.Hostname())
	}
	return nil
}

// FetchLatest fetches the latest stable release of the given "owner/repo".
func FetchLatest(ctx context.Context, c *http.Client, repo string) (*Release, error) {
	latestURL := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return nil, err
	}
	if err := checkURL(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "counterweight-recontools")

	resp, err := c.Do(req)
	if err != nil {
		return nil, locale.Errorf("GitHub request failed (configure the global proxy in settings if needed): %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusTooManyRequests:
		return nil, locale.NewError("GitHub API rate limit reached (60 requests/hour); try again later")
	case resp.StatusCode == http.StatusNotFound:
		return nil, locale.Errorf("Repository %s has no stable releases", repo)
	case resp.StatusCode != http.StatusOK:
		return nil, locale.Errorf("GitHub returned %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, locale.Errorf("Parse release: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, locale.NewError("Release is missing a tag")
	}
	return &rel, nil
}

// FindAsset looks up a release asset by exact, then case-insensitive, name match.
func (r *Release) FindAsset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Asset{}, false
}

// FindChecksums finds the release's checksum manifest. ProjectDiscovery's
// goreleaser configs name it inconsistently (some default to
// "<project>_<version>_checksums.txt", one names it "<project>-checksums.txt"),
// so match loosely on the word "checksum" rather than a fixed filename.
func (r *Release) FindChecksums() (Asset, bool) {
	for _, a := range r.Assets {
		if strings.Contains(strings.ToLower(a.Name), "checksum") {
			return a, true
		}
	}
	return Asset{}, false
}
