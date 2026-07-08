package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Source is a discovery adapter. Discover walks its corpus and hands every
// candidate issue to ingest. Adapters own their cursors: watchlist advances
// repos.last_scan_at only after that repo's window ingested cleanly, so a crash
// mid-scan just rescans the window idempotently.
type Source interface {
	Name() string
	Discover(c *Client, p *Pool, cfg *Config, ingest func(*Item) error) error
}

// cursorOverlap is subtracted from the stored cursor when building the since
// parameter, so clock skew or a mid-page crash cannot drop an update.
const cursorOverlap = 10 * time.Minute

type watchlistSource struct{}

func (watchlistSource) Name() string { return "watchlist" }

func (watchlistSource) Discover(c *Client, p *Pool, cfg *Config, ingest func(*Item) error) error {
	for _, wr := range cfg.Watchlist.Repos {
		windowStart := time.Now().UTC()
		var meta struct {
			Archived bool `json:"archived"`
		}
		if _, err := c.get("/repos/"+wr.Name, &meta); err != nil {
			return err
		}
		if err := p.UpsertRepo(wr.Name, wr.Weight, meta.Archived); err != nil {
			return err
		}
		cur, err := p.RepoCursor(wr.Name)
		if err != nil {
			return err
		}
		u := fmt.Sprintf("/repos/%s/issues?state=open&sort=updated&direction=desc&per_page=100", wr.Name)
		if cur != "" {
			if t, err := time.Parse(timeFmt, cur); err == nil {
				u += "&since=" + url.QueryEscape(t.Add(-cursorOverlap).Format(timeFmt))
			}
		}
		for pages := 0; u != "" && pages < cfg.Watchlist.MaxPages; pages++ {
			var raw []ghIssue
			next, err := c.get(u, &raw)
			if err != nil {
				return err
			}
			for _, gi := range raw {
				if gi.PullRequest != nil {
					continue // the issues endpoint also returns PRs
				}
				it := gi.toItem(wr.Name, "watchlist", meta.Archived)
				if err := ingest(&it); err != nil {
					return err
				}
			}
			u = next
		}
		if err := p.SetRepoCursor(wr.Name, windowStart.Format(timeFmt)); err != nil {
			return err
		}
	}
	return nil
}

type searchSource struct{}

func (searchSource) Name() string { return "search" }

func (searchSource) Discover(c *Client, p *Pool, cfg *Config, ingest func(*Item) error) error {
	since := time.Now().UTC().AddDate(0, 0, -cfg.Search.UpdatedWithinDays).Format("2006-01-02")
	// The stars:>N search qualifier is broken on the issues search backend, so the
	// star floor is enforced here from repo metadata, cached per run.
	type repoMeta struct {
		Stars    int  `json:"stargazers_count"`
		Archived bool `json:"archived"`
	}
	metas := map[string]repoMeta{}
	for _, q := range cfg.Search.Queries {
		u := fmt.Sprintf("/search/issues?advanced_search=true&sort=updated&order=desc&per_page=%d&q=%s",
			cfg.Search.PerPage, url.QueryEscape(q+" updated:>="+since))
		var res struct {
			Items []ghIssue `json:"items"`
		}
		if _, err := c.get(u, &res); err != nil {
			return err
		}
		for _, gi := range res.Items {
			if gi.PullRequest != nil {
				continue
			}
			repo := strings.TrimPrefix(gi.RepositoryURL, apiBase+"/repos/")
			meta, ok := metas[repo]
			if !ok {
				if _, err := c.get("/repos/"+repo, &meta); err != nil {
					return err
				}
				metas[repo] = meta
			}
			if meta.Stars < cfg.Search.MinStars {
				continue
			}
			it := gi.toItem(repo, "search", meta.Archived)
			if err := ingest(&it); err != nil {
				return err
			}
		}
	}
	return nil
}

// fetchRecentComments loads the bodies of the last page of comments (30 max).
// Called only for gate survivors with comments > 0.
func fetchRecentComments(c *Client, it *Item) error {
	page := (it.Comments + 29) / 30
	u := fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=30&page=%d", it.Repo, it.Number, page)
	var cs []struct {
		Body string `json:"body"`
	}
	if _, err := c.get(u, &cs); err != nil {
		return err
	}
	it.RecentComments = it.RecentComments[:0]
	for _, x := range cs {
		it.RecentComments = append(it.RecentComments, x.Body)
	}
	return nil
}

// fetchLinkedPRCount counts open PRs that cross-reference the issue, via the
// timeline API. Called only for gate survivors. Capped at 3 pages; issues that
// busy are quarantined by the comment threshold anyway.
func fetchLinkedPRCount(c *Client, it *Item) error {
	u := fmt.Sprintf("/repos/%s/issues/%d/timeline?per_page=100", it.Repo, it.Number)
	count := 0
	for pages := 0; u != "" && pages < 3; pages++ {
		var events []struct {
			Event  string `json:"event"`
			Source struct {
				Issue *struct {
					State       string    `json:"state"`
					PullRequest *struct{} `json:"pull_request"`
				} `json:"issue"`
			} `json:"source"`
		}
		next, err := c.get(u, &events)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Event == "cross-referenced" && e.Source.Issue != nil &&
				e.Source.Issue.PullRequest != nil && e.Source.Issue.State == "open" {
				count++
			}
		}
		u = next
	}
	it.LinkedPRCount = count
	return nil
}

// ghIssue is the GitHub wire format, shared by the issues list and search endpoints.
type ghIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	HTMLURL   string    `json:"html_url"`
	State     string    `json:"state"`
	Locked    bool      `json:"locked"`
	Comments  int       `json:"comments"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      ghUser    `json:"user"`
	Assignee  *ghUser   `json:"assignee"`
	Labels    []ghLabel `json:"labels"`
	Reactions struct {
		TotalCount int `json:"total_count"`
	} `json:"reactions"`
	PullRequest   *struct{} `json:"pull_request"`
	RepositoryURL string    `json:"repository_url"`
}

type ghUser struct {
	Login string `json:"login"`
}

type ghLabel struct {
	Name string `json:"name"`
}

func (g ghIssue) toItem(repo, source string, archived bool) Item {
	labels := make([]string, 0, len(g.Labels))
	for _, l := range g.Labels {
		labels = append(labels, l.Name)
	}
	assignee := ""
	if g.Assignee != nil {
		assignee = g.Assignee.Login
	}
	return Item{
		Repo:         repo,
		Number:       g.Number,
		Title:        g.Title,
		URL:          g.HTMLURL,
		Labels:       labels,
		Author:       g.User.Login,
		CreatedAt:    g.CreatedAt.UTC(),
		UpdatedAt:    g.UpdatedAt.UTC(),
		Comments:     g.Comments,
		Reactions:    g.Reactions.TotalCount,
		Assignee:     assignee,
		State:        g.State,
		Locked:       g.Locked,
		RepoArchived: archived,
		Source:       source,
	}
}
