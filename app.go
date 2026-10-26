package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
	"github.com/psdcoder/agentdiff/internal/store"
)

const commitsShown = 50

type fileDiff struct {
	change  gitx.FileChange
	state   *editor.DiffState
	err     string
	loading bool
}

// overviewRun is an overview being written; it belongs to the review it
// was started for, so switching scopes leaves it running.
type overviewRun struct {
	text     string
	activity string
	cancel   context.CancelFunc
}

type app struct {
	win *mygo.Window
	// queue takes the updates of a window-less app when a test wants to
	// run them on its own goroutine.
	queue chan func()
	store store.Store
	cfg   store.Config

	repo    *gitx.Repo
	repoErr string
	branch  string
	// head is the branch picked to compare with base; empty means the
	// checked-out one.
	head     string
	base     string
	branches []string
	commits  []gitx.Commit

	scope    gitx.Scope
	scopeID  string
	rev      *review.Review
	files    []gitx.FileChange
	diffstat string
	loading  bool
	loadErr  string
	diffs    map[string]*fileDiff
	gen      int

	reviews   map[string]*review.Review
	runs      map[*review.Review][]*logRun
	logOpen   bool
	logTarget string
	logPct    float32

	page      string
	history   []string
	forward   []string
	scrolls   map[string]*ui.ScrollState
	thread    string
	drafts    map[string]string
	overviews map[*review.Review]*overviewRun
	threadRun map[string]context.CancelFunc
	walkRuns  map[string]*walkRun
	ahead     []queuedWalk

	sidebarW    float32
	scopePct    float32
	mainPct     float32
	groupsOpen  map[string]*bool
	md          markdown.Options
	exportAgent bool
	exportDone  bool
	savedPath   string
	askDraft    string
	question    string
	asking      *askTarget
	paneDraft   string
	extra       map[string]*extraFile
	reveal      *reveal

	settings    *settingsForm
	rangeForm   *rangeForm
	compareForm *compareForm
}

func newApp(s store.Store) *app {
	return &app{
		store:       s,
		cfg:         s.LoadConfig(),
		diffs:       map[string]*fileDiff{},
		reviews:     map[string]*review.Review{},
		runs:        map[*review.Review][]*logRun{},
		logPct:      50,
		drafts:      map[string]string{},
		overviews:   map[*review.Review]*overviewRun{},
		threadRun:   map[string]context.CancelFunc{},
		walkRuns:    map[string]*walkRun{},
		extra:       map[string]*extraFile{},
		groupsOpen:  map[string]*bool{},
		sidebarW:    280,
		scopePct:    34,
		mainPct:     62,
		page:        "overview",
		scrolls:     map[string]*ui.ScrollState{},
		exportAgent: true,
	}
}

// update runs fn on the main thread. Tests have no window and run it at
// once, or put it on queue.
func (a *app) update(fn func()) {
	switch {
	case a.win != nil:
		a.win.Update(fn)
	case a.queue != nil:
		a.queue <- fn
	default:
		fn()
	}
}

func (a *app) async(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	go fn()
}

func (a *app) openRepo(path string) {
	r, err := gitx.Open(path)
	if err != nil {
		a.repoErr = err.Error()
		return
	}
	a.repo, a.repoErr = r, ""
	a.cfg.AddRecent(r.Root)
	a.saveConfig()
	a.branch, a.head, a.base, a.branches, a.commits = "", "", "", nil, nil
	if a.win != nil {
		a.win.SetTitle("AgentDiff: " + r.Name())
	}
	a.async(func() {
		ctx := context.Background()
		branch, _ := r.CurrentBranch(ctx)
		base := r.DefaultBase(ctx)
		branches, _ := r.Branches(ctx)
		a.update(func() {
			if a.repo != r {
				return
			}
			a.branch, a.base, a.branches = branch, base, branches
			a.loadCommits()
		})
	})
	a.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
}

// headBranch is the branch under review: the picked one, or the one
// checked out.
func (a *app) headBranch() string { return cmp.Or(a.head, a.branch) }

// commitBase is the base the commit list is relative to: on a feature
// branch the commits not on the base yet, else the latest commits.
func (a *app) commitBase() string {
	head := a.headBranch()
	if a.base == "" || head == "" || a.base == head || strings.HasSuffix(a.base, "/"+head) {
		return ""
	}
	return a.base
}

// branchScope is the changes of the branch under review since its base.
func (a *app) branchScope() (gitx.Scope, bool) {
	base := a.commitBase()
	if base == "" {
		return gitx.Scope{}, false
	}
	return a.branchScopeOf(a.headBranch(), base), true
}

// branchScopeOf is the changes of a branch since base. The checked-out
// branch is read from HEAD, any other one by its name.
func (a *app) branchScopeOf(branch, base string) gitx.Scope {
	if branch == a.branch {
		return gitx.Scope{Kind: gitx.ScopeBranch, Base: base, Branch: branch}
	}
	return gitx.Scope{Kind: gitx.ScopeBranch, Base: base, Head: branch}
}

// syncBranch reads the checked-out branch again, to follow a checkout
// made outside the app.
func (a *app) syncBranch() {
	r := a.repo
	if r == nil {
		return
	}
	a.async(func() {
		ctx := context.Background()
		branch, err := r.CurrentBranch(ctx)
		if err != nil {
			log.Printf("current branch: %v", err)
			return
		}
		branches, _ := r.Branches(ctx)
		a.update(func() {
			if a.repo == r && branch != a.branch {
				a.checkedOut(branch, branches)
			}
		})
	})
}

// checkedOut moves the app to a newly checked-out branch. A branch scope
// keeps reviewing the same branch, so its review stays.
func (a *app) checkedOut(branch string, branches []string) {
	a.branch, a.branches = branch, branches
	if s := a.scope; s.Kind == gitx.ScopeBranch && (s.Head != "" || s.Branch != "") {
		name := cmp.Or(s.Head, s.Branch)
		a.head = name
		if name == branch {
			a.head = ""
		}
		a.scope = a.branchScopeOf(name, s.Base)
		a.rev.Scope = a.scope
	} else if a.head == branch {
		a.head = ""
	}
	a.loadCommits()
	a.reload()
}

func (a *app) refresh() {
	a.reload()
	a.syncBranch()
}

func (a *app) loadCommits() {
	r, base, head := a.repo, a.commitBase(), a.head
	a.async(func() {
		commits, err := r.Commits(context.Background(), base, head, commitsShown)
		if err != nil {
			log.Printf("commits: %v", err)
		}
		a.update(func() {
			if a.repo == r {
				a.commits = commits
			}
		})
	})
}

func scopeID(s gitx.Scope) string { return "scope:" + s.Key() }

func reviewKey(repo string, s gitx.Scope) string { return repo + "\x00" + s.Key() }

// reviewOf loads a review once and keeps it, so an agent run that finishes
// after a scope switch writes into the review the view shows.
func (a *app) reviewOf(s gitx.Scope) *review.Review {
	key := reviewKey(a.repo.Root, s)
	r := a.reviews[key]
	if r == nil {
		r = a.store.LoadReview(a.repo.Root, s)
		a.reviews[key] = r
	}
	return r
}

func (a *app) setScope(s gitx.Scope) {
	a.scope, a.scopeID = s, scopeID(s)
	a.rev = a.reviewOf(s)
	a.rev.Scope = s
	a.resetNav()
	a.thread = ""
	a.exportDone, a.savedPath = false, ""
	a.reload()
}

// reload reads the changes of the scope again, keeping the review.
func (a *app) reload() {
	a.gen++
	gen, r, s := a.gen, a.repo, a.scope
	a.loading, a.loadErr = true, ""
	a.files, a.diffstat = nil, ""
	a.diffs = map[string]*fileDiff{}
	a.extra = map[string]*extraFile{}
	a.async(func() {
		ctx := context.Background()
		files, err := r.Changes(ctx, s)
		stat := ""
		if err == nil {
			stat, err = r.DiffStat(ctx, s)
		}
		a.update(func() {
			if gen != a.gen {
				return
			}
			a.loading = false
			if err != nil {
				a.loadErr = err.Error()
				return
			}
			a.files, a.diffstat = files, stat
			for _, f := range files {
				a.diffs[f.Path] = &fileDiff{change: f, loading: true}
			}
			if f, ok := strings.CutPrefix(a.page, "file:"); ok && a.diffs[f] == nil {
				a.page = "overview"
			}
		})
		if err != nil {
			return
		}
		for _, f := range files {
			oldText, newText, cerr := r.Contents(ctx, s, f)
			a.update(func() {
				if gen != a.gen {
					return
				}
				a.setContents(f, oldText, newText, cerr)
			})
		}
	})
}

func (a *app) setContents(f gitx.FileChange, oldText, newText string, err error) {
	d := a.diffs[f.Path]
	if d == nil {
		return
	}
	d.loading = false
	switch {
	case errors.Is(err, gitx.ErrBinary):
		d.err = "Binary file"
		return
	case errors.Is(err, gitx.ErrTooLarge):
		d.err = "File too large to show"
		return
	case err != nil:
		d.err = err.Error()
		return
	}
	lang := editor.LanguageFor(f.Path)
	before, after := editor.NewBuffer(oldText), editor.NewBuffer(newText)
	before.SetLanguage(lang)
	after.SetLanguage(lang)
	d.state = &editor.DiffState{Old: before, New: after, LineSelection: true, Wrap: false}
}

func (a *app) fileNames() []string {
	out := make([]string, len(a.files))
	for i, f := range a.files {
		out[i] = f.Path
	}
	return out
}

func (a *app) groups() []review.Group {
	if a.rev.Overview != nil && len(a.rev.Overview.Groups) > 0 {
		return review.NormalizeGroups(a.rev.Overview.Groups, a.fileNames())
	}
	return review.DirectoryGroups(a.fileNames())
}

// startOver forgets the review of the current scope: its overview,
// threads and viewed files.
func (a *app) startOver() {
	a.stopOverview()
	for _, t := range a.rev.Threads {
		a.stopThread(t)
	}
	for _, w := range a.rev.Walks {
		a.stopWalk(w.ID)
	}
	if err := a.store.DeleteReview(a.repo.Root, a.scope); err != nil {
		log.Printf("delete review: %v", err)
	}
	delete(a.runs, a.rev)
	a.rev = review.New(a.repo.Root, a.scope)
	a.reviews[reviewKey(a.repo.Root, a.scope)] = a.rev
	a.thread, a.savedPath = "", ""
	a.resetNav()
	a.drafts = map[string]string{}
}

func (a *app) confirmStartOver() {
	a.confirm("Start this review over?", "The overview, the deeper walkthroughs, all comments and threads, and the viewed marks of this scope are deleted.", "Start Over", a.startOver)
}

// confirm runs fn after the user agrees in a warning dialog. Without a
// window, in tests, it runs fn at once.
func (a *app) confirm(message, detail, button string, fn func()) {
	if a.win == nil {
		fn()
		return
	}
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: a.win, Type: mygo.MessageWarning,
			Message: message, Detail: detail,
			Buttons: []string{button, "Cancel"},
		})
		if err == nil && res.Button == 0 {
			a.update(fn)
		}
	}()
}

func (a *app) saveReview() {
	if a.rev == nil {
		return
	}
	if err := a.store.SaveReview(a.rev); err != nil {
		log.Printf("save review: %v", err)
	}
}

func (a *app) saveReviewOf(r *review.Review) {
	if err := a.store.SaveReview(r); err != nil {
		log.Printf("save review: %v", err)
	}
}

func (a *app) saveConfig() {
	if err := a.store.SaveConfig(a.cfg); err != nil {
		log.Printf("save config: %v", err)
	}
}

func (a *app) scopeLabel() string {
	switch a.scope.Kind {
	case gitx.ScopeCommit:
		for _, c := range a.commits {
			if c.SHA == a.scope.Commit {
				return fmt.Sprintf("%s %s", c.Short, c.Subject)
			}
		}
	}
	return a.scope.Label()
}

func (a *app) openThread(id string) {
	a.closeEmptyThread(id)
	a.thread, a.question, a.asking = id, "", nil
}

// closeEmptyThread drops a thread the user opened and left without
// writing anything, unless it is the one being opened.
func (a *app) closeEmptyThread(keep string) {
	if a.thread == "" || a.thread == keep || a.rev == nil {
		return
	}
	if t := a.rev.Thread(a.thread); t != nil && len(t.Messages) == 0 {
		a.rev.DeleteThread(t.ID)
		delete(a.drafts, t.ID)
	}
}

func (a *app) newThread(anchor review.Anchor) {
	t := a.rev.AddThread(anchor)
	a.openThread(t.ID)
}

func (a *app) diffOf(path string) *fileDiff { return a.diffs[path] }

// quoteLines returns lines start..end (1-based) of a side of a file.
func (a *app) quoteLines(path string, side review.Side, start, end int) string {
	d := a.diffs[path]
	if d == nil || d.state == nil {
		return ""
	}
	buf := d.state.New
	if side == review.SideOld {
		buf = d.state.Old
	}
	var lines []string
	for i := start - 1; i < end && i < buf.LineCount(); i++ {
		if i >= 0 {
			lines = append(lines, buf.Line(i))
		}
	}
	return strings.Join(lines, "\n")
}

func sideOf(s editor.DiffSide) review.Side {
	if s == editor.DiffOld {
		return review.SideOld
	}
	return review.SideNew
}

func diffSide(s review.Side) editor.DiffSide {
	if s == review.SideOld {
		return editor.DiffOld
	}
	return editor.DiffNew
}
