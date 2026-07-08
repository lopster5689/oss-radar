package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Watchlist struct {
		MaxPages int         `yaml:"max_pages"`
		Repos    []WatchRepo `yaml:"repos"`
	} `yaml:"watchlist"`
	Search struct {
		PerPage           int      `yaml:"per_page"`
		UpdatedWithinDays int      `yaml:"updated_within_days"`
		MinStars          int      `yaml:"min_stars"`
		Queries           []string `yaml:"queries"`
	} `yaml:"search"`
	Gate struct {
		PositiveLabels      []string `yaml:"positive_labels"`
		DisqualifyingLabels []string `yaml:"disqualifying_labels"`
		SkillKeywords       []string `yaml:"skill_keywords"`
		Quarantine          struct {
			AgeDays     int `yaml:"age_days"`
			MaxComments int `yaml:"max_comments"`
		} `yaml:"quarantine"`
		ClaimPatterns     []string `yaml:"claim_patterns"`
		AmbiguousPatterns []string `yaml:"ambiguous_patterns"`
	} `yaml:"gate"`
}

type WatchRepo struct {
	Name   string  `yaml:"name"`
	Weight float64 `yaml:"weight"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(c.Watchlist.Repos) == 0 {
		return nil, errors.New("config: watchlist.repos is empty")
	}
	if c.Watchlist.MaxPages <= 0 {
		c.Watchlist.MaxPages = 2
	}
	if c.Search.PerPage <= 0 {
		c.Search.PerPage = 50
	}
	if c.Search.UpdatedWithinDays <= 0 {
		c.Search.UpdatedWithinDays = 30
	}
	if c.Search.MinStars <= 0 {
		c.Search.MinStars = 500
	}
	if c.Gate.Quarantine.AgeDays <= 0 || c.Gate.Quarantine.MaxComments <= 0 {
		return nil, errors.New("config: gate.quarantine thresholds must be set")
	}
	return &c, nil
}

// GateConfig compiles the gate section for the given evaluation time.
// All claim patterns are made case-insensitive here.
func (c *Config) GateConfig(now time.Time) (GateConfig, error) {
	claim, err := compileAll(c.Gate.ClaimPatterns)
	if err != nil {
		return GateConfig{}, err
	}
	ambiguous, err := compileAll(c.Gate.AmbiguousPatterns)
	if err != nil {
		return GateConfig{}, err
	}
	return GateConfig{
		PositiveLabels:      c.Gate.PositiveLabels,
		DisqualifyingLabels: c.Gate.DisqualifyingLabels,
		SkillKeywords:       c.Gate.SkillKeywords,
		ClaimPatterns:       claim,
		AmbiguousPatterns:   ambiguous,
		MaxAgeDays:          c.Gate.Quarantine.AgeDays,
		MaxComments:         c.Gate.Quarantine.MaxComments,
		Now:                 now,
	}, nil
}

func compileAll(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}
