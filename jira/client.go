// Package jira is a small client for the Jira Cloud REST API (v3): the calls
// Counterweight needs to file security findings as issues under an epic and to
// keep those issues updated. Authentication is an Atlassian account email plus
// an API token (HTTP Basic), the documented method for server-side scripts.
package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTP is the transport. Redirects are not followed, so the API token is only
// ever sent to the configured site.
var HTTP = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// AllowLoopbackHTTP admits http://127.0.0.1 sites. Only tests set it: a real
// site is always reached over HTTPS.
var AllowLoopbackHTTP = false

// maxRetryAfter is the longest Retry-After honored inline; longer throttling
// fails the call so the caller can try again later.
const maxRetryAfter = 60 * time.Second

// maxRetries bounds retries after 429 or 503 responses.
const maxRetries = 3

// Config identifies a Jira Cloud site and the account that acts on it.
type Config struct {
	Site  string // https://<name>.atlassian.net
	Email string
	Token string
}

// Client calls one Jira Cloud site.
type Client struct {
	site, email, token string
	// sleep waits between retries; tests shorten it.
	sleep func(context.Context, time.Duration) error
}

// NormalizeSite validates a site URL and returns it without a trailing slash.
// HTTPS is required; plain HTTP is accepted only for a loopback host (tests).
func NormalizeSite(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("enter the Jira site URL, for example https://your-company.atlassian.net")
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return "", errors.New("use the site root URL without a path, for example https://your-company.atlassian.net")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if ip := net.ParseIP(u.Hostname()); !AllowLoopbackHTTP || ip == nil || !ip.IsLoopback() {
			return "", errors.New("the Jira site must use https")
		}
	default:
		return "", errors.New("the Jira site must use https")
	}
	return u.Scheme + "://" + u.Host, nil
}

// New returns a client after validating the configuration.
func New(cfg Config) (*Client, error) {
	site, err := NormalizeSite(cfg.Site)
	if err != nil {
		return nil, err
	}
	email := strings.TrimSpace(cfg.Email)
	token := strings.TrimSpace(cfg.Token)
	if email == "" || !strings.Contains(email, "@") || len(email) > 254 {
		return nil, errors.New("enter the email address of the Atlassian account that owns the API token")
	}
	if token == "" || len(token) > 1024 || strings.ContainsAny(token, " \r\n\t") {
		return nil, errors.New("enter a valid Atlassian API token")
	}
	return &Client{site: site, email: email, token: token, sleep: sleepCtx}, nil
}

// Site is the normalized site URL.
func (c *Client) Site() string { return c.site }

// BrowseURL is the web address of an issue.
func (c *Client) BrowseURL(key string) string { return c.site + "/browse/" + url.PathEscape(key) }

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// APIError is a non-2xx reply, with Jira's messages joined for display.
type APIError struct {
	Status  int
	Method  string
	Path    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Jira %s %s: HTTP %d", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("Jira %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
}

// IsNotFound reports a 404 from Jira (deleted issue, or no permission to see it).
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// errorMessage flattens Jira's {"errorMessages":[], "errors":{field: msg}} reply.
func errorMessage(data []byte) string {
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(data, &e) != nil {
		return ""
	}
	parts := append([]string{}, e.ErrorMessages...)
	for field, msg := range e.Errors {
		parts = append(parts, field+": "+msg)
	}
	return strings.Join(parts, "; ")
}

// do sends one request and decodes a JSON reply into out. 429 replies (which
// Jira rejects before processing) are retried for every method; 503 only for
// reads, since a POST may have taken effect. Retry-After is honored.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.site+path, rd)
		if err != nil {
			return err
		}
		req.SetBasicAuth(c.email, c.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Counterweight")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("Jira request failed: %w", err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		retryable := resp.StatusCode == http.StatusTooManyRequests ||
			(resp.StatusCode == http.StatusServiceUnavailable && method == http.MethodGet)
		if retryable && attempt < maxRetries {
			wait := time.Duration(attempt+1) * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
				wait = time.Duration(s) * time.Second
			}
			if wait <= maxRetryAfter {
				if err := c.sleep(ctx, wait); err != nil {
					return err
				}
				continue
			}
		}
		if resp.StatusCode >= 300 {
			msg := errorMessage(data)
			if resp.StatusCode == http.StatusUnauthorized {
				msg = "the email or API token was rejected"
			}
			return &APIError{Status: resp.StatusCode, Method: method, Path: strings.SplitN(path, "?", 2)[0], Message: msg}
		}
		if out != nil && len(data) > 0 {
			return json.Unmarshal(data, out)
		}
		return nil
	}
}

// User is the account the token belongs to.
type User struct {
	AccountID    string `json:"accountId"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
}

// Myself checks the credentials and names the account.
func (c *Client) Myself(ctx context.Context) (User, error) {
	var u User
	err := c.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &u)
	return u, err
}

// Project is a Jira project (space) the account can see.
type Project struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
	// Style is "classic" (company-managed) or "next-gen" (team-managed).
	Style string `json:"style"`
}

// Projects lists the projects the account can browse, at most 500.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	out := []Project{}
	for start := 0; start < 500; {
		var page struct {
			Values []Project `json:"values"`
			IsLast bool      `json:"isLast"`
		}
		path := "/rest/api/3/project/search?orderBy=name&maxResults=50&startAt=" + strconv.Itoa(start)
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Values...)
		if page.IsLast || len(page.Values) == 0 {
			break
		}
		start += len(page.Values)
	}
	return out, nil
}

// IssueType is an issue type available for creation in a project.
type IssueType struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Subtask        bool   `json:"subtask"`
	HierarchyLevel int    `json:"hierarchyLevel"` // 1 is the epic level, 0 standard, -1 subtask
}

// IssueTypes lists the issue types the account can create in a project.
func (c *Client) IssueTypes(ctx context.Context, project string) ([]IssueType, error) {
	out := []IssueType{}
	for start := 0; start < 1000; {
		var page struct {
			IssueTypes []IssueType `json:"issueTypes"`
			Values     []IssueType `json:"values"`
			Total      int         `json:"total"`
		}
		path := "/rest/api/3/issue/createmeta/" + url.PathEscape(project) + "/issuetypes?maxResults=100&startAt=" + strconv.Itoa(start)
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		types := page.IssueTypes
		if len(types) == 0 {
			types = page.Values
		}
		out = append(out, types...)
		start += len(types)
		if len(types) == 0 || page.Total == 0 || start >= page.Total {
			break
		}
	}
	return out, nil
}

// AllowedValue is one option of a field (priority, security level...).
type AllowedValue struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Field describes a field on the create screen of an issue type.
type Field struct {
	FieldID       string         `json:"fieldId"`
	Key           string         `json:"key"`
	Name          string         `json:"name"`
	Required      bool           `json:"required"`
	HasDefault    bool           `json:"hasDefaultValue"`
	AllowedValues []AllowedValue `json:"allowedValues"`
}

// CreateFields lists the fields on the create screen of a project's issue type,
// keyed by field id.
func (c *Client) CreateFields(ctx context.Context, project, issueTypeID string) (map[string]Field, error) {
	out := map[string]Field{}
	for start := 0; start < 1000; {
		var page struct {
			Fields []Field `json:"fields"`
			Values []Field `json:"values"`
			Total  int     `json:"total"`
		}
		path := "/rest/api/3/issue/createmeta/" + url.PathEscape(project) + "/issuetypes/" + url.PathEscape(issueTypeID) +
			"?maxResults=100&startAt=" + strconv.Itoa(start)
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		fields := page.Fields
		if len(fields) == 0 {
			fields = page.Values
		}
		for _, f := range fields {
			id := f.FieldID
			if id == "" {
				id = f.Key
			}
			out[id] = f
		}
		start += len(fields)
		if len(fields) == 0 || (page.Total > 0 && start >= page.Total) || page.Total == 0 {
			break
		}
	}
	return out, nil
}

// Issue identifies a created issue.
type Issue struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// CreateIssue creates one issue from a fields object.
func (c *Client) CreateIssue(ctx context.Context, fields map[string]any) (Issue, error) {
	var out Issue
	err := c.do(ctx, http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": fields}, &out)
	return out, err
}

// UpdateIssue edits fields of an existing issue.
func (c *Client) UpdateIssue(ctx context.Context, key string, fields map[string]any) error {
	return c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), map[string]any{"fields": fields}, nil)
}

// IssueStatus is the workflow status of an issue.
type IssueStatus struct {
	Key      string
	Name     string
	Category string // new, indeterminate or done
}

// Status reads an issue's current status.
func (c *Client) Status(ctx context.Context, key string) (IssueStatus, error) {
	var out struct {
		Key    string `json:"key"`
		Fields struct {
			Status struct {
				Name           string `json:"name"`
				StatusCategory struct {
					Key string `json:"key"`
				} `json:"statusCategory"`
			} `json:"status"`
		} `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?fields=status", nil, &out); err != nil {
		return IssueStatus{}, err
	}
	return IssueStatus{Key: out.Key, Name: out.Fields.Status.Name, Category: out.Fields.Status.StatusCategory.Key}, nil
}

// AddComment posts an ADF comment.
func (c *Client) AddComment(ctx context.Context, key string, body Doc) error {
	return c.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", map[string]any{"body": body}, nil)
}

// Transition is a workflow move available from an issue's current status.
type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   struct {
		Name           string `json:"name"`
		StatusCategory struct {
			Key string `json:"key"`
		} `json:"statusCategory"`
	} `json:"to"`
}

// Transitions lists the transitions available for an issue now.
func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	var out struct {
		Transitions []Transition `json:"transitions"`
	}
	err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", nil, &out)
	return out.Transitions, err
}

// TransitionTo moves an issue to the first status in the given category
// ("done", "indeterminate" or "new"). It returns the status name reached, or
// "" when the workflow offers no such transition from the current status.
// Workflows differ between projects, so categories are used instead of names.
func (c *Client) TransitionTo(ctx context.Context, key string, categories ...string) (string, error) {
	ts, err := c.Transitions(ctx, key)
	if err != nil {
		return "", err
	}
	for _, cat := range categories {
		for _, t := range ts {
			if t.To.StatusCategory.Key == cat {
				body := map[string]any{"transition": map[string]string{"id": t.ID}}
				if err := c.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", body, nil); err != nil {
					return "", err
				}
				return t.To.Name, nil
			}
		}
	}
	return "", nil
}

// AddRemoteLink attaches a web link to an issue. Jira keeps one link per
// globalId on an issue, so repeating the call updates instead of duplicating.
func (c *Client) AddRemoteLink(ctx context.Context, key, globalID, linkURL, title string) error {
	body := map[string]any{"globalId": globalID, "object": map[string]string{"url": linkURL, "title": title}}
	return c.do(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/remotelink", body, nil)
}
