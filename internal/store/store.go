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
	"strings"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

const maxRecent = 10

type Config struct {
	Agent agent.Config `json:"agent"`
	// Prompts are the prompts the user rewrote in the settings.
	Prompts review.Prompts `json:"prompts,omitempty"`
	Unified bool           `json:"unified,omitempty"`
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
	return Config{Agent: agent.Config{Provider: agent.Claude, AutoEffort: "low"}, DeeperAhead: true}
}

func (config *Config) AddRecent(repo string) {
	config.Recent = slices.DeleteFunc(slices.Clone(config.Recent), func(r string) bool { return r == repo })
	config.Recent = append([]string{repo}, config.Recent...)
	if len(config.Recent) > maxRecent {
		config.Recent = config.Recent[:maxRecent]
	}
}

func (config *Config) RemoveRecent(repo string) {
	config.Recent = slices.DeleteFunc(slices.Clone(config.Recent), func(r string) bool { return r == repo })
}

type Store struct{ Dir string }

// LoadConfig returns the saved config, or the default one. A config file
// it cannot read is moved to config.json.bad.
func (store Store) LoadConfig() Config {
	path := filepath.Join(store.Dir, "config.json")
	file := struct {
		Config
		Questions string `json:"questions"`
	}{Config: DefaultConfig()}
	if err := readJSON(path, &file); err != nil {
		setAside(path, err)
		return DefaultConfig()
	}
	cfg := file.Config
	if cfg.Agent.Provider == "" {
		cfg.Agent.Provider = agent.Claude
	}
	if questions := strings.TrimSpace(file.Questions); questions != "" && questions != oldDefaultQuestions && questions != legacyQuestions && cfg.Prompts[review.PromptOverview] == "" {
		if cfg.Prompts == nil {
			cfg.Prompts = review.Prompts{}
		}
		cfg.Prompts[review.PromptOverview] = review.DefaultOverview(questions)
	}
	return cfg
}

// oldDefaultQuestions and legacyQuestions were the default checklist
// questions, which a config held before the overview prompt took them.
// A config that still holds one of them keeps the default prompt.
const (
	oldDefaultQuestions = `1. What data changes: schemas, storage, formats?
2. What UI changes, and how do I see them?
3. What architecture and API changes?
4. Which changes are not beautiful?
5. Which assumptions and rules force the non-beautiful changes?`
	legacyQuestions = `Explain the changes:

1. Explain on high level.
2. Explain data changes.
3. Explain UI changes and how to see them.
4. Explain architecture and API changes.
5. Explain non-beautiful changes.
6. Tell about assumptions and rules which force non-beautiful changes.`
)

func (store Store) SaveConfig(cfg Config) error {
	return writeJSON(filepath.Join(store.Dir, "config.json"), cfg)
}

func (store Store) reviewPath(repo string, scope gitx.Scope) string {
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(store.Dir, "reviews", hex.EncodeToString(sum[:8]), scope.Key()+".json")
}

// LoadReview returns the saved review of a scope, or a new one. A review
// file it cannot read is moved to the same path with ".bad" added.
func (store Store) LoadReview(repo string, scope gitx.Scope) *review.Review {
	path := store.reviewPath(repo, scope)
	loaded := review.New(repo, scope)
	if err := readJSON(path, loaded); err != nil {
		setAside(path, err)
		return review.New(repo, scope)
	}
	if loaded.Viewed == nil {
		loaded.Viewed = map[string]bool{}
	}
	loaded.Repo, loaded.Scope = repo, scope
	return loaded
}

func (store Store) SaveReview(saving *review.Review) error {
	saving.Updated = time.Now()
	return writeJSON(store.reviewPath(saving.Repo, saving.Scope), saving)
}

// DeleteReview deletes a review together with the logs of its agent runs.
func (store Store) DeleteReview(repo string, scope gitx.Scope) error {
	err := os.Remove(store.reviewPath(repo, scope))
	if IsNotExist(err) {
		err = nil
	}
	return errors.Join(err, os.RemoveAll(store.runDir(repo, scope)))
}

// FindReview returns the newest saved review of repo, other than the one
// of scope, whose overview was written for the changes with patchID.
func (store Store) FindReview(repo string, scope gitx.Scope, patchID string) (*review.Review, bool) {
	paths, _ := filepath.Glob(filepath.Join(filepath.Dir(store.reviewPath(repo, scope)), "*.json"))
	var found *review.Review
	for _, path := range paths {
		saved, ok := readReview(path, repo)
		if !ok || saved.Overview == nil || saved.Overview.PatchID != patchID || saved.Scope.Key() == scope.Key() {
			continue
		}
		if found == nil || saved.Updated.After(found.Updated) {
			found = saved
		}
	}
	return found, found != nil
}

// MoveReview moves a review and the logs of its runs to another scope.
// It replaces what the other scope had.
func (store Store) MoveReview(repo string, from, to gitx.Scope) error {
	if err := store.DeleteReview(repo, to); err != nil {
		return err
	}
	if err := os.Rename(store.reviewPath(repo, from), store.reviewPath(repo, to)); err != nil {
		return err
	}
	if err := os.Rename(store.runDir(repo, from), store.runDir(repo, to)); err != nil && !IsNotExist(err) {
		return err
	}
	return store.rescope(repo, to)
}

// CopyReview copies a review and the logs of its runs to another scope.
// It replaces what the other scope had.
func (store Store) CopyReview(repo string, from, to gitx.Scope) error {
	if err := store.DeleteReview(repo, to); err != nil {
		return err
	}
	if err := copyFile(store.reviewPath(repo, from), store.reviewPath(repo, to)); err != nil {
		return err
	}
	runs, err := os.ReadDir(store.runDir(repo, from))
	if err != nil && !IsNotExist(err) {
		return err
	}
	if len(runs) > 0 {
		if err := os.MkdirAll(store.runDir(repo, to), 0o755); err != nil {
			return err
		}
	}
	for _, e := range runs {
		if err := copyFile(filepath.Join(store.runDir(repo, from), e.Name()), filepath.Join(store.runDir(repo, to), e.Name())); err != nil {
			return err
		}
	}
	return store.rescope(repo, to)
}

// rescope writes the scope into a review that moved or was copied there,
// so FindReview reads where it is.
func (store Store) rescope(repo string, scope gitx.Scope) error {
	return writeJSON(store.reviewPath(repo, scope), store.LoadReview(repo, scope))
}

// maxArchived is how many archived reviews a repository keeps.
const maxArchived = 20

// ArchiveReview moves the review of the uncommitted changes, which are
// gone from the working tree, under id.
func (store Store) ArchiveReview(repo, id string) (gitx.Scope, error) {
	to := gitx.ArchivedScope(id)
	if err := store.MoveReview(repo, gitx.Scope{Kind: gitx.ScopeUncommitted}, to); err != nil {
		return to, err
	}
	// Saving marks the time of archiving, which orders the archive.
	return to, store.SaveReview(store.LoadReview(repo, to))
}

// PruneArchive deletes the oldest archived reviews of repo past
// maxArchived.
func (store Store) PruneArchive(repo string) error {
	archived := store.Archived(repo)
	var errs []error
	for _, r := range archived[min(len(archived), maxArchived):] {
		errs = append(errs, store.DeleteReview(repo, r.Scope))
	}
	return errors.Join(errs...)
}

// Archived returns the archived reviews of repo that have an overview,
// the newest first.
func (store Store) Archived(repo string) []*review.Review {
	pattern := filepath.Join(filepath.Dir(store.reviewPath(repo, gitx.Scope{})), "archived-*.json")
	paths, _ := filepath.Glob(pattern)
	var out []*review.Review
	for _, path := range paths {
		saved, ok := readReview(path, repo)
		if !ok || saved.Scope.Kind != gitx.ScopeArchived || saved.Overview == nil {
			continue
		}
		out = append(out, saved)
	}
	slices.SortFunc(out, func(a, b *review.Review) int { return b.Updated.Compare(a.Updated) })
	return out
}

// readReview reads a saved review of repo for a scan of the reviews,
// which skips a file it cannot read. The review keeps the scope its file
// names.
func readReview(path, repo string) (*review.Review, bool) {
	saved := &review.Review{}
	if readJSON(path, saved) != nil {
		return nil, false
	}
	if saved.Viewed == nil {
		saved.Viewed = map[string]bool{}
	}
	saved.Repo = repo
	return saved, true
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
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// writeJSON writes through a temporary file, so a crash never leaves a
// half-written file behind.
func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
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
	_, err = tmp.Write(data)
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
