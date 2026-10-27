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
	// DeeperAhead writes the deeper walkthrough of every overview step
	// right after the overview.
	DeeperAhead bool     `json:"deeperAhead"`
	Recent      []string `json:"recent,omitempty"`
}

func DefaultConfig() Config {
	return Config{Agent: agent.Config{Provider: agent.Claude, AutoEffort: "low"}, Questions: review.DefaultQuestions, DeeperAhead: true}
}

func (c *Config) AddRecent(repo string) {
	c.Recent = slices.DeleteFunc(c.Recent, func(r string) bool { return r == repo })
	c.Recent = append([]string{repo}, c.Recent...)
	if len(c.Recent) > maxRecent {
		c.Recent = c.Recent[:maxRecent]
	}
}

func (c *Config) RemoveRecent(repo string) {
	c.Recent = slices.DeleteFunc(c.Recent, func(r string) bool { return r == repo })
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
	if cfg.Questions == review.LegacyQuestions {
		cfg.Questions = review.DefaultQuestions
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
	if q := r.Settings.Questions; q != nil && *q == review.LegacyQuestions {
		r.Settings.Questions = nil
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

// FindReview returns the scope of the newest saved review of repo, other
// than of scope, whose overview was written for the changes with patchID.
func (s Store) FindReview(repo string, scope gitx.Scope, patchID string) (gitx.Scope, bool) {
	paths, _ := filepath.Glob(filepath.Join(filepath.Dir(s.reviewPath(repo, scope)), "*.json"))
	var found gitx.Scope
	var newest time.Time
	for _, path := range paths {
		var r struct {
			Scope    gitx.Scope `json:"scope"`
			Updated  time.Time  `json:"updated"`
			Overview *struct {
				PatchID string `json:"patchId"`
			} `json:"overview"`
		}
		if readJSON(path, &r) != nil || r.Overview == nil || r.Overview.PatchID != patchID || r.Scope.Key() == scope.Key() {
			continue
		}
		if found.Kind == "" || r.Updated.After(newest) {
			found, newest = r.Scope, r.Updated
		}
	}
	return found, found.Kind != ""
}

// MoveReview moves a review and the logs of its runs to another scope.
// It replaces what the other scope had.
func (s Store) MoveReview(repo string, from, to gitx.Scope) error {
	if err := s.DeleteReview(repo, to); err != nil {
		return err
	}
	if err := os.Rename(s.reviewPath(repo, from), s.reviewPath(repo, to)); err != nil {
		return err
	}
	if err := os.Rename(s.runDir(repo, from), s.runDir(repo, to)); err != nil && !IsNotExist(err) {
		return err
	}
	return s.rescope(repo, to)
}

// CopyReview copies a review and the logs of its runs to another scope.
// It replaces what the other scope had.
func (s Store) CopyReview(repo string, from, to gitx.Scope) error {
	if err := s.DeleteReview(repo, to); err != nil {
		return err
	}
	if err := copyFile(s.reviewPath(repo, from), s.reviewPath(repo, to)); err != nil {
		return err
	}
	runs, err := os.ReadDir(s.runDir(repo, from))
	if err != nil && !IsNotExist(err) {
		return err
	}
	if len(runs) > 0 {
		if err := os.MkdirAll(s.runDir(repo, to), 0o755); err != nil {
			return err
		}
	}
	for _, e := range runs {
		if err := copyFile(filepath.Join(s.runDir(repo, from), e.Name()), filepath.Join(s.runDir(repo, to), e.Name())); err != nil {
			return err
		}
	}
	return s.rescope(repo, to)
}

// rescope writes the scope into a review that moved or was copied there,
// so FindReview reads where it is.
func (s Store) rescope(repo string, scope gitx.Scope) error {
	return writeJSON(s.reviewPath(repo, scope), s.LoadReview(repo, scope))
}

// maxArchived is how many archived reviews a repository keeps.
const maxArchived = 20

// ArchiveReview moves the review of the uncommitted changes, which are
// gone from the working tree, under id. It deletes the oldest archived
// reviews past maxArchived.
func (s Store) ArchiveReview(repo, id string) (gitx.Scope, error) {
	to := gitx.Scope{Kind: gitx.ScopeArchived, From: id}
	if err := s.MoveReview(repo, gitx.Scope{Kind: gitx.ScopeUncommitted}, to); err != nil {
		return to, err
	}
	// Saving marks the time of archiving, which orders the archive.
	if err := s.SaveReview(s.LoadReview(repo, to)); err != nil {
		return to, err
	}
	archived := s.archived(repo)
	var errs []error
	for _, r := range archived[min(len(archived), maxArchived):] {
		errs = append(errs, s.DeleteReview(repo, r.Scope))
	}
	return to, errors.Join(errs...)
}

// LatestArchive returns the archived review that was updated last.
func (s Store) LatestArchive(repo string) (*review.Review, bool) {
	archived := s.archived(repo)
	if len(archived) == 0 {
		return nil, false
	}
	return archived[0], true
}

// archived returns the archived reviews of repo, the newest first.
func (s Store) archived(repo string) []*review.Review {
	pattern := filepath.Join(filepath.Dir(s.reviewPath(repo, gitx.Scope{})), "archived-*.json")
	paths, _ := filepath.Glob(pattern)
	var out []*review.Review
	for _, path := range paths {
		r := &review.Review{}
		if readJSON(path, r) == nil && r.Scope.Kind == gitx.ScopeArchived {
			out = append(out, s.LoadReview(repo, r.Scope))
		}
	}
	slices.SortFunc(out, func(a, b *review.Review) int { return b.Updated.Compare(a.Updated) })
	return out
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o600)
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
