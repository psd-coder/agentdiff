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

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

const maxRecent = 10

type Config struct {
	Agent     agent.Config `json:"agent"`
	Questions string       `json:"questions"`
	Unified   bool         `json:"unified,omitempty"`
	// FlatFiles lists the files in the sidebar by path instead of in
	// their review groups.
	FlatFiles bool `json:"flatFiles,omitempty"`
	// HideViewed leaves the viewed files out of the sidebar.
	HideViewed bool `json:"hideViewed,omitempty"`
	// RecentCommands are the commands run from the command palette, the
	// last first.
	RecentCommands []string `json:"recentCommands,omitempty"`
	// Keys are the shortcuts the user gave commands, by command id, in
	// place of the app's defaults. An empty value removes the shortcut.
	Keys map[string]string `json:"keys,omitempty"`
	// DeeperAhead writes the deeper walkthrough of every overview step
	// right after the overview.
	DeeperAhead bool `json:"deeperAhead"`
	// Theme is "light" or "dark", or empty to follow the system.
	Theme string `json:"theme,omitempty"`
	// CodeTheme is the name of the theme that colors code, empty for the
	// default one.
	CodeTheme string   `json:"codeTheme,omitempty"`
	Recent    []string `json:"recent,omitempty"`
}

func DefaultConfig() Config {
	return Config{Agent: agent.Config{Provider: agent.Claude, AutoEffort: "low"}, Questions: review.DefaultQuestions, DeeperAhead: true}
}

func (c *Config) AddRecent(repo string) {
	c.Recent = slices.DeleteFunc(slices.Clone(c.Recent), func(r string) bool { return r == repo })
	c.Recent = append([]string{repo}, c.Recent...)
	if len(c.Recent) > maxRecent {
		c.Recent = c.Recent[:maxRecent]
	}
}

func (c *Config) RemoveRecent(repo string) {
	c.Recent = slices.DeleteFunc(slices.Clone(c.Recent), func(r string) bool { return r == repo })
}

type Store struct{ Dir string }

// LoadConfig returns the saved config, or the default one. A config file
// it cannot read is moved to config.json.bad.
func (s Store) LoadConfig() Config {
	path := filepath.Join(s.Dir, "config.json")
	cfg := DefaultConfig()
	if err := readJSON(path, &cfg); err != nil {
		setAside(path, err)
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

// LoadReview returns the saved review of a scope, or a new one. A review
// file it cannot read is moved to the same path with ".bad" added.
func (s Store) LoadReview(repo string, scope gitx.Scope) *review.Review {
	path := s.reviewPath(repo, scope)
	r := review.New(repo, scope)
	if err := readJSON(path, r); err != nil {
		setAside(path, err)
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

// FindReview returns the newest saved review of repo, other than the one
// of scope, whose overview was written for the changes with patchID.
func (s Store) FindReview(repo string, scope gitx.Scope, patchID string) (*review.Review, bool) {
	paths, _ := filepath.Glob(filepath.Join(filepath.Dir(s.reviewPath(repo, scope)), "*.json"))
	var found *review.Review
	for _, path := range paths {
		r, ok := readReview(path, repo)
		if !ok || r.Overview == nil || r.Overview.PatchID != patchID || r.Scope.Key() == scope.Key() {
			continue
		}
		if found == nil || r.Updated.After(found.Updated) {
			found = r
		}
	}
	return found, found != nil
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
	to := gitx.ArchivedScope(id)
	if err := s.MoveReview(repo, gitx.Scope{Kind: gitx.ScopeUncommitted}, to); err != nil {
		return to, err
	}
	// Saving marks the time of archiving, which orders the archive.
	if err := s.SaveReview(s.LoadReview(repo, to)); err != nil {
		return to, err
	}
	archived := s.Archived(repo)
	var errs []error
	for _, r := range archived[min(len(archived), maxArchived):] {
		errs = append(errs, s.DeleteReview(repo, r.Scope))
	}
	return to, errors.Join(errs...)
}

// Archived returns the archived reviews of repo that have an overview,
// the newest first.
func (s Store) Archived(repo string) []*review.Review {
	pattern := filepath.Join(filepath.Dir(s.reviewPath(repo, gitx.Scope{})), "archived-*.json")
	paths, _ := filepath.Glob(pattern)
	var out []*review.Review
	for _, path := range paths {
		r, ok := readReview(path, repo)
		if !ok || r.Scope.Kind != gitx.ScopeArchived || r.Overview == nil {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *review.Review) int { return b.Updated.Compare(a.Updated) })
	return out
}

// readReview reads a saved review of repo for a scan of the reviews,
// which skips a file it cannot read. The review keeps the scope its file
// names.
func readReview(path, repo string) (*review.Review, bool) {
	r := &review.Review{}
	if readJSON(path, r) != nil {
		return nil, false
	}
	if r.Viewed == nil {
		r.Viewed = map[string]bool{}
	}
	r.Repo = repo
	return r, true
}

// setAside moves a file that failed to load with err to path.bad, so the
// next save does not write over data the app cannot read, for example a
// file from another version of the app. It keeps only the newest .bad file.
func setAside(path string, err error) {
	if !IsNotExist(err) {
		_ = os.Rename(path, path+".bad")
	}
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
	_, err = tmp.Write(b)
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}

func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
