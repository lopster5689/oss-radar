package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

// Client is a minimal GitHub REST v3 client. It counts every request so a run
// can report exactly what it spent, and it self-throttles on rate-limit headers.
type Client struct {
	hc    *http.Client
	token string
	calls int
}

func NewClient() (*Client, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		out, err := exec.Command("gh", "auth", "token").Output()
		if err != nil {
			return nil, fmt.Errorf("GITHUB_TOKEN unset and gh auth token failed: %w", err)
		}
		token = strings.TrimSpace(string(out))
	}
	if token == "" {
		return nil, errors.New("empty github token")
	}
	return &Client{hc: &http.Client{Timeout: 30 * time.Second}, token: token}, nil
}

func (c *Client) Calls() int { return c.calls }

// get fetches u (a path like /repos/x/y or an absolute next-page URL), decodes the
// JSON body into v and returns the rel="next" URL from the Link header, if any.
func (c *Client) get(u string, v any) (next string, err error) {
	if strings.HasPrefix(u, "/") {
		u = apiBase + u
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "oss-radar/0.1")

		resp, err := c.hc.Do(req)
		if err != nil {
			return "", err
		}
		c.calls++
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		c.throttle(resp.Header)

		switch {
		case resp.StatusCode == http.StatusOK:
			if err := json.Unmarshal(body, v); err != nil {
				return "", fmt.Errorf("decode %s: %w", u, err)
			}
			return nextLink(resp.Header.Get("Link")), nil
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
			// Abort politely: cursors were not advanced, a re-run resumes cleanly.
			return "", fmt.Errorf("github refused %s (%s, retry-after=%s, remaining=%s); stopping this run",
				u, resp.Status, resp.Header.Get("Retry-After"), resp.Header.Get("X-RateLimit-Remaining"))
		case resp.StatusCode >= 500 && attempt == 0:
			time.Sleep(2 * time.Second)
			continue
		default:
			return "", fmt.Errorf("GET %s: %s: %s", u, resp.Status, truncate(string(body), 200))
		}
	}
}

// throttle sleeps until the rate window resets when headroom gets low. The search
// resource only has 30 req/min, so its floor is far lower than core's.
func (c *Client) throttle(h http.Header) {
	rem, err := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if err != nil {
		return
	}
	floor := 100
	if h.Get("X-RateLimit-Resource") == "search" {
		floor = 5
	}
	if rem >= floor {
		return
	}
	reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return
	}
	d := time.Until(time.Unix(reset, 0)) + 5*time.Second
	if d > 0 {
		fmt.Fprintf(os.Stderr, "rate limit low (%d left), sleeping %s\n", rem, d.Round(time.Second))
		time.Sleep(d)
	}
}

func nextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		seg := strings.Split(part, ";")
		if len(seg) < 2 || !strings.Contains(seg[1], `rel="next"`) {
			continue
		}
		return strings.Trim(strings.TrimSpace(seg[0]), "<>")
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
