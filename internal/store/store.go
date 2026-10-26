package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
)

const maxRecent = 10

type Config struct {
	Agent     agent.Config `json:"agent"`
	Questions string       `json:"questions"`
	Unified   bool         `json:"unified,omitempty"`
	Recent    []string     `json:"recent,omitempty"`
}

func DefaultConfig() Config {
	return Config{Agent: agent.Config{Provider: agent.Claude}, Questions: review.DefaultQuestions}
}

func (c *Config) AddRecent(repo string) {
	c.Recent = slices.DeleteFunc(c.Recent, func(r string) bool { return r == repo })
	c.Recent = append([]string{repo}, c.Recent...)
	if len(c.Recent) > maxRecent {
		c.Recent = c.Recent[:maxRecent]
	}
}

// Effective is the configuration a review runs with: its own settings
// over the global ones.
func (c Config) Effective(s review.Settings) (agent.Config, string) {
	a, q := c.Agent, c.Questions
	if s.Agent != nil {
		a = *s.Agent
	}
	if s.Questions != nil {
		q = *s.Questions
	}
	return a, q
}

type Store struct{ Dir string }

func (s Store) LoadConfig() Config {
	cfg := DefaultConfig()
	if err := readJSON(filepath.Join(s.Dir, "config.json"), &cfg); err != nil {
		return DefaultConfig()
	}
	if cfg.Agent.Provider == "" {
		cfg.Agent.Provider = agent.Claude
	}
	return cfg
}

func (s Store) SaveConfig(cfg Config) error {
	return writeJSON(filepath.Join(s.Dir, "config.json"), cfg)
}

func (s Store) reviewPath(repo string, scope gitx.Scope) string {
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(s.Dir, "reviews", hex.EncodeToString(sum[:8]), scope.Key()+".json")
}

// LoadReview returns the saved review of a scope, or a new one.
func (s Store) LoadReview(repo string, scope gitx.Scope) *review.Review {
	r := review.New(repo, scope)
	if err := readJSON(s.reviewPath(repo, scope), r); err != nil {
		return review.New(repo, scope)
	}
	if r.Viewed == nil {
		r.Viewed = map[string]bool{}
	}
	r.Repo, r.Scope = repo, scope
	return r
}

func (s Store) SaveReview(r *review.Review) error {
	r.Updated = time.Now()
	return writeJSON(s.reviewPath(r.Repo, r.Scope), r)
}

// DeleteReview deletes a review together with the logs of its agent runs.
func (s Store) DeleteReview(repo string, scope gitx.Scope) error {
	err := os.Remove(s.reviewPath(repo, scope))
	if IsNotExist(err) {
		err = nil
	}
	return errors.Join(err, os.RemoveAll(s.runDir(repo, scope)))
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON writes through a temporary file, so a crash never leaves a
// half-written file behind.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
