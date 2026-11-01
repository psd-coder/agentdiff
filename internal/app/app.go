package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/continuity"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

const (
	// commitPage is how many commits the commit list reads at a time.
	commitPage = 50
	// commitsAhead is how close to the last commit read the list scrolls
	// before it reads the next page.
	commitsAhead = 10
)

type fileDiff struct {
	change  gitx.FileChange
	state   *editor.DiffState
	err     string
	loading bool
}

type app struct {
	win *mygo.Window
	// queue takes the updates of a window-less app when a test wants to
	// run them on its own goroutine.
	queue chan func()
	store store.Store
	cfg   store.Config

	// watchFiles starts the watches that follow the open project, none
	// when nil.
	watchFiles watchFunc
	stopWatch  context.CancelFunc
	// startAgent starts every agent run.
	startAgent agentFunc
	// askConfirm replaces the confirm dialog in tests, when set.
	askConfirm func(message, detail, button string, fn func())

	repo    *gitx.Repo
	repoErr string
	cmpr    comparison
	commits commitList
	// changeList is the place of the list of changes in the sidebar.
	changeList ui.ListState

	scope gitx.Scope
	// chosen is the scope the user opened last, which the list of
	// changes shows chosen while it loads.
	chosen gitx.Scope
	rev    *review.Review
	loader *scopeLoader
	perLoad
	// grouped caches groups, which a frame reads several times.
	grouped groupsCache

	reviews   *continuity.Reviews
	logOpen   bool
	logTarget string
	logPct    float32

	perReview
	// drafts holds the unsent text of each thread and question for the
	// session, by questionDraftKey or thread ID.
	drafts map[string]string
	runner *runner
	// streams counts the goroutines that stream agent runs into the store,
	// so a test can wait for them before its store is removed.
	streams sync.WaitGroup
	// logs is the run log of each review, newest first, in memory.
	logs    map[*review.Review][]*logRun
	targets *targets

	sidebarW    float32
	scopePct    float32
	mainPct     float32
	groupsOpen  map[string]*bool
	md          markdown.Options
	codeKey     codeThemeKey
	codeColors  *editor.Theme
	exportAgent bool
	reveal      *reveal

	settings     *settingsForm
	compareForm  *compareForm
	projectsOpen bool
	projectNav   pickList
	branchesOpen bool
	branchQuery  string
	branchNav    pickList
	// refocus names the element that takes the keyboard focus in the
	// next frame: a picker button once its list closed after a choice
	// ("project" or "branch"), or a sidebar ("changes" or "files").
	refocus       string
	shortcutsOpen bool

	cmdList      []*command
	cmds         map[string]*command
	inMenu       map[string]bool
	menuDirty    bool
	km           *keymap
	pending      string // the command the menu bar queued
	paletteOpen  bool
	paletteQuery string
	paletteNav   pickList
	// recording is the command whose shortcut the settings record.
	recording   string
	recordStart bool
	keysFilter  string
	keysNote    string
	// saveErr is why the last write of a review or the settings failed;
	// it clears once a later write works.
	saveErr string
	// promptProblems are the prompt files of the open repository that the
	// last run could not use.
	promptProblems []string
}

// perReview is the state of the window for the review it shows. Leaving
// the review replaces all of it, see leaveReview.
type perReview struct {
	nav     navigation
	scrolls map[string]*ui.ScrollState
	lists   map[page]*cardPage
	panel   sidePanel
	// exportDone includes the resolved threads in the feedback, and
	// savedPath is where the feedback was saved last.
	exportDone bool
	savedPath  string
	// folded holds the files the reviewer folded or unfolded. The others
	// are folded while they are viewed or big.
	folded map[string]bool
}

func newPerReview() perReview {
	return perReview{
		scrolls: map[string]*ui.ScrollState{},
		lists:   map[page]*cardPage{},
		folded:  map[string]bool{},
	}
}

// sidePanel is what the side panel shows: a panelThread, a panelQuestion
// or a panelAsk. It is nil while the panel is closed.
type sidePanel interface{ isSidePanel() }

// panelThread is the ID of the thread the panel shows.
type panelThread string

// panelQuestion is the ID of the question walk the panel shows.
type panelQuestion string

// panelAsk is the target of a new question the panel asks.
type panelAsk askTarget

func (panelThread) isSidePanel()   {}
func (panelQuestion) isSidePanel() {}
func (panelAsk) isSidePanel()      {}

// threadID is the thread the side panel shows, or "".
func (app *app) threadID() string {
	id, _ := app.panel.(panelThread)
	return string(id)
}

// questionID is the question walk the side panel shows, or "".
func (app *app) questionID() string {
	id, _ := app.panel.(panelQuestion)
	return string(id)
}

// asking is the target of the new question the side panel asks, or nil.
func (app *app) asking() *askTarget {
	if target, ok := app.panel.(panelAsk); ok {
		return (*askTarget)(&target)
	}
	return nil
}

// perLoad is what a load read about the changes of the scope. A new load
// replaces all of it.
type perLoad struct {
	// snap is the snapshot the load read. Files outside the changes are
	// read from it too.
	snap     *gitx.Snapshot
	files    []gitx.FileChange
	diffstat string
	loadErr  string
	// fp identifies the changes of the scope once patchKnown.
	fp         gitx.Fingerprint
	patchKnown bool
	diffs      map[string]*fileDiff
	// extra holds the files the code snippets show that are not changed.
	extra map[string]*extraFile
	// commit is the commit under review, for a commit scope.
	commit *gitx.Commit
	// movedTo is the commit that now holds the changes a stale overview
	// of the uncommitted changes was written for.
	movedTo string
}

func newPerLoad() perLoad {
	return perLoad{diffs: map[string]*fileDiff{}, extra: map[string]*extraFile{}}
}

func newApp(storage store.Store) *app {
	app := &app{
		store:       storage,
		cfg:         storage.LoadConfig(),
		startAgent:  agent.Run,
		perLoad:     newPerLoad(),
		perReview:   newPerReview(),
		reviews:     continuity.New(storage),
		logPct:      50,
		drafts:      map[string]string{},
		logs:        map[*review.Review][]*logRun{},
		groupsOpen:  map[string]*bool{},
		sidebarW:    280,
		scopePct:    34,
		mainPct:     62,
		exportAgent: true,
	}
	app.runner = newRunner(app.stream, app.saveReviewOf)
	app.loader = newScopeLoader(app.reviews, app.async, app.update, app)
	app.targets = newTargets(app.runner, app.onScreen)
	return app
}

// update runs fn on the main thread. Tests have no window and run it at
// once, or put it on queue.
func (app *app) update(fn func()) {
	switch {
	case app.win != nil:
		app.win.Update(fn)
	case app.queue != nil:
		app.queue <- fn
	default:
		fn()
	}
}

func (app *app) async(fn func()) {
	if app.win == nil {
		fn()
		return
	}
	go fn()
}

func (app *app) openRepo(path string) {
	repo, err := gitx.Open(path)
	if err != nil {
		app.repoErr = err.Error()
		return
	}
	app.repo, app.repoErr = repo, ""
	app.prompts(repo.Root)
	app.watch(repo)
	app.cfg.AddRecent(repo.Root)
	app.saveConfig()
	app.cmpr = comparison{}
	app.commits.reset()
	app.loader.reset()
	if app.win != nil {
		app.win.SetTitle("AgentDiff: " + repo.Name())
	}
	app.async(func() {
		ctx := context.Background()
		branch, err := repo.CurrentBranch(ctx)
		base := repo.DefaultBase(ctx)
		branches, trees, lerr := readBranches(ctx, repo)
		err = cmp.Or(err, lerr)
		app.update(func() {
			if app.repo != repo {
				return
			}
			app.cmpr.branch, app.cmpr.base, app.cmpr.branches, app.cmpr.worktrees = branch, base, branches, trees
			app.cmpr.err = errText(err)
			app.loadCommits()
		})
	})
	app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
}

// closeRepo goes back to the welcome screen.
func (app *app) closeRepo() {
	app.repo, app.promptProblems = nil, nil
	app.unwatch()
	app.loader.reset()
	app.perLoad = newPerLoad()
	app.projectsOpen = false
	app.commits.reset()
	if app.win != nil {
		app.win.SetTitle("AgentDiff")
	}
}

// compare reviews branch against base, as picked in the Compare dialog.
func (app *app) compare(branch, base string) {
	scope, all := app.cmpr.compare(branch, base)
	app.showComparison(scope, all)
}

// pickBranch lists the commits of branch and opens all its changes.
// Without a base to compare with, it opens the uncommitted changes of
// the checked-out branch, or the newest commit of another one.
func (app *app) pickBranch(branch string) {
	scope, all := app.cmpr.pick(branch)
	app.showComparison(scope, all)
}

func (app *app) showComparison(scope gitx.Scope, all bool) {
	app.commits.newest = !all && app.cmpr.head != ""
	app.loadCommits()
	switch {
	case all:
		app.setScope(scope)
	case app.cmpr.head == "":
		app.setScope(gitx.Scope{Kind: gitx.ScopeUncommitted})
	}
}

// setRange reviews the changes between two revisions.
func (app *app) setRange(from, to string) {
	scope := app.cmpr.setRange(from, to)
	app.loadCommits()
	app.setScope(scope)
}

// syncBranch reads the checked-out branch again, to follow a checkout
// made outside the app.
func (app *app) syncBranch() {
	repo := app.repo
	if repo == nil {
		return
	}
	app.async(func() {
		ctx := context.Background()
		branch, err := repo.CurrentBranch(ctx)
		if err != nil {
			app.update(func() {
				if app.repo == repo {
					app.cmpr.err = err.Error()
				}
			})
			return
		}
		branches, trees, err := readBranches(ctx, repo)
		app.update(func() {
			if app.repo != repo {
				return
			}
			app.cmpr.err = errText(err)
			app.cmpr.worktrees = trees
			if branch != app.cmpr.branch {
				app.checkedOut(branch, branches)
			} else {
				app.cmpr.branches = branches
			}
		})
	})
}

// readBranches lists the branches and the worktrees of r, and returns
// the first error.
func readBranches(ctx context.Context, repo *gitx.Repo) ([]string, []gitx.Worktree, error) {
	branches, err := repo.Branches(ctx)
	trees, terr := repo.Worktrees(ctx)
	return branches, trees, cmp.Or(err, terr)
}

// errText is the message of err, or "" when err is nil.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// checkedOut moves the app to a newly checked-out branch. A branch scope
// keeps reviewing the same branch, so its review stays.
func (app *app) checkedOut(branch string, branches []string) {
	if to, ok := app.cmpr.checkedOut(branch, branches, app.scope); ok && app.reviews.Rescope(app.rev, to) {
		app.scope = to
	}
	app.loadCommits()
	app.reload()
}

func (app *app) refresh() {
	if app.repo == nil {
		return
	}
	app.reload()
	app.loadCommits()
	app.syncBranch()
}

// loadCommits reads the commit list again, for the range in view.
func (app *app) loadCommits() {
	repo := app.repo
	base, head := app.cmpr.listRange()
	gen, limit := app.commits.read(repo.Root + "\x00" + base + ".." + cmp.Or(head, app.cmpr.branch))
	app.async(func() {
		ctx := context.Background()
		cl, err := repo.CommitLog(ctx, base, head)
		total := 0
		var commits []gitx.Commit
		if err == nil {
			total, err = cl.Count(ctx)
		}
		if err == nil {
			commits, err = cl.Page(ctx, 0, limit)
		}
		app.update(func() {
			if sha := app.commits.listed(gen, cl, commits, total, err); sha != "" {
				app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: sha})
			}
		})
	})
}

// readCommitPage reads the page after the commits read, unless a read
// runs. last is the last commit in view.
func (app *app) readCommitPage(last int) {
	gen, from, ok := app.commits.readPage(last)
	if !ok {
		return
	}
	cl := app.commits.log
	app.async(func() {
		page, err := cl.Page(context.Background(), from, commitPage)
		app.update(func() { app.commits.paged(gen, page, err) })
	})
}

func (app *app) setScope(scope gitx.Scope) {
	app.commits.newest = false
	app.chosen = scope
	app.load(scope, true)
}

// reload reads the changes of the scope again, keeping the review: of
// the scope being loaded, if one is.
func (app *app) reload() {
	loadAgain := app.loader.again(app.scope)
	app.load(loadAgain.scope, loadAgain.fresh)
}

// enterScope makes s, with its review rev, the scope the window shows.
func (app *app) enterScope(scope gitx.Scope, rev *review.Review) {
	if app.leaveReview() {
		app.saveReview()
	}
	app.reviews.ClearAdopted()
	app.scope, app.rev = scope, rev
	app.targets.seen(rev, review.TargetOverview)
}

// load reads the changes of s, with the files the first page shows.
// Until they land the window keeps what it shows, under a spinner. A new
// scope opens with its own review on its overview.
func (app *app) load(scope gitx.Scope, fresh bool) {
	rev, page := app.rev, app.nav.page
	if fresh {
		rev, page = app.reviews.Of(app.repo.Root, scope), overview
	}
	if !app.loader.shown {
		if fresh {
			app.enterScope(scope, rev)
		}
		app.perLoad = newPerLoad()
	}
	app.loader.start(loadRequest{
		repo: app.repo, scopeLoad: scopeLoad{scope, fresh}, rev: rev,
		first: app.firstPaint(rev, page, fresh),
	})
}

// scopeLanded shows the changes a load read.
func (app *app) scopeLanded(ld landing) {
	if ld.enter {
		app.enterScope(ld.scope, ld.rev)
	}
	app.perLoad = ld.perLoad
	if app.loadErr != "" {
		return
	}
	app.settle(ld.cont.Decide(app.rev, app.runner.reviewBusy))
	for p, c := range ld.first {
		if app.diffs[p] != nil {
			app.setContents(c.change, c.oldText, c.newText, c.err)
		} else {
			app.extra[p] = newExtraFile(c.newText, c.err)
		}
	}
	app.leaveGone()
}

func (app *app) commitsLocated(movedTo string) { app.movedTo = movedTo }

// fileRead shows a file a load read after it landed: a changed file, or
// a file outside the changes that a code snippet shows.
func (app *app) fileRead(fileRead contents) {
	if app.diffs[fileRead.change.Path] != nil {
		app.setContents(fileRead.change, fileRead.oldText, fileRead.newText, fileRead.err)
		return
	}
	app.extra[fileRead.change.Path] = newExtraFile(fileRead.newText, fileRead.err)
	if app.keepSnippets() {
		app.saveReview()
	}
}

// firstPaint are the files the page shows first: the code snippets of
// an overview or a walkthrough, the file of a file page, the files of a
// group page.
func (app *app) firstPaint(rev *review.Review, page page, fresh bool) []string {
	var paths []string
	walk := func(w *review.Walkthrough) {
		if w == nil {
			return
		}
		for _, s := range w.Steps {
			for _, r := range s.Refs {
				paths = append(paths, r.File)
			}
		}
		for _, sec := range w.Sections {
			for _, it := range sec.Items {
				for _, r := range it.Refs {
					paths = append(paths, r.File)
				}
			}
		}
	}
	switch {
	case page.kind == kindOverview && rev.Overview != nil:
		walk(rev.Overview.Walk)
	case page.kind == kindWalk:
		if deeperWalk := rev.Walk(page.key); deeperWalk != nil {
			walk(deeperWalk.Content)
		}
	case page.kind == kindFile:
		paths = append(paths, page.key)
	case page.kind == kindGroup && !fresh:
		paths = append(paths, app.groupFiles(page.key)...)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// settle applies a decision on the review of the scope. It runs while
// a.rev is still the review the decision started from. A failure goes to
// the notice, which stays, because a load runs it each time.
func (app *app) settle(out continuity.Outcome) {
	for _, r := range out.Dropped {
		app.release(r)
	}
	if old := out.Archived; old != nil {
		if app.leaveReview() {
			app.saved(app.store.SaveReview(old))
		}
	}
	app.rev = out.Rev
	if out.Err != nil {
		app.saveErr = out.Err.Error()
	}
}

// restoreArchived brings an archived review back to the uncommitted
// scope, which keeps it from then on.
func (app *app) restoreArchived(earlier *continuity.Earlier) {
	app.showNext(app.reviews.Restore(earlier, app.rev, app.runner.reviewBusy), "Could not restore the review")
}

// moveArchived moves an archived review to the commit that probably
// holds its changes, when that commit has no review of its own, and
// opens the commit.
func (app *app) moveArchived(earlier *continuity.Earlier) {
	to := gitx.Scope{Kind: gitx.ScopeCommit, Commit: earlier.Probably}
	app.showNext(app.reviews.MoveTo(app.repo.Root, earlier, to, app.runner.reviewBusy), "Could not move the review")
}

func (app *app) deleteArchived(earlier *continuity.Earlier) {
	app.showNext(app.reviews.Delete(app.repo.Root, earlier), "Could not delete the review")
}

// showNext shows what an action on an archived review did. When the
// action replaced the review of the scope, its error goes to the notice
// with the other save errors. Any other error goes to a dialog that says
// failed.
func (app *app) showNext(next continuity.Next, failed string) {
	if next.Rev != nil {
		app.settle(next.Outcome)
	} else if next.Err != nil {
		app.showError(failed, next.Err)
	}
	if next.Left {
		app.leaveGone()
	}
	if next.Open == nil {
		return
	}
	if *next.Open == app.scope {
		app.openPage(overview)
	} else {
		app.setScope(*next.Open)
	}
}

func (app *app) setContents(change gitx.FileChange, oldText, newText string, err error) {
	diff := app.diffs[change.Path]
	if diff == nil {
		return
	}
	diff.loading = false
	switch {
	case errors.Is(err, gitx.ErrBinary):
		diff.err = "Binary file"
		return
	case errors.Is(err, gitx.ErrTooLarge):
		diff.err = "File too large to show"
		return
	case err != nil:
		diff.err = err.Error()
		return
	}
	lang := editor.LanguageFor(change.Path)
	before, after := editor.NewBuffer(oldText), editor.NewBuffer(newText)
	before.SetLanguage(lang)
	after.SetLanguage(lang)
	diff.state = &editor.DiffState{Old: before, New: after, LineSelection: true, Wrap: false}
	if app.keepSnippets() {
		app.saveReview()
	}
}

// keepSnippets saves the code of the snippets of the review into it,
// while the changes are the ones the review was written for. An archived
// review shows this code after the changes are gone. It reports whether
// it saved any.
func (app *app) keepSnippets() bool {
	if app.freshness() != overviewFresh {
		return false
	}
	kept := false
	app.rev.EachRef(func(r *review.Ref) {
		if len(r.Code) > 0 {
			return
		}
		loaded, ok := app.loadedSnippet(*r)
		if !ok || loaded.loading || loaded.missing != "" || len(loaded.lines) == 0 {
			return
		}
		for i, line := range loaded.lines {
			r.Code = append(r.Code, review.CodeLine{Text: line, Changed: loaded.changed[i]})
		}
		kept = true
	})
	return kept
}

func (app *app) fileNames() []string {
	out := make([]string, len(app.files))
	for i, f := range app.files {
		out[i] = f.Path
	}
	return out
}

// fileOrder is the order the sidebar lists the files in.
func (app *app) fileOrder() []string {
	if app.cfg.FlatFiles {
		return app.fileNames()
	}
	var files []string
	for _, g := range app.groups() {
		files = append(files, g.Files...)
	}
	return files
}

// listed tells whether the sidebar lists the file.
func (app *app) listed(file string) bool {
	return !app.cfg.HideViewed || !app.rev.Viewed[file]
}

// setViewed marks the files viewed and folds them, or marks them not
// viewed and unfolds them, except big files, which stay folded.
func (app *app) setViewed(files []string, viewed bool) {
	for _, f := range files {
		delete(app.folded, f)
		app.rev.Viewed[f] = viewed
	}
	app.saveReview()
}

// isFolded reports whether the card of the file shows only its header.
func (app *app) isFolded(file string) bool {
	if folded, ok := app.folded[file]; ok {
		return folded
	}
	if diff := app.diffs[file]; diff != nil && diff.change.Additions+diff.change.Deletions > bigFileLines {
		return true
	}
	return app.rev.Viewed[file]
}

// groups are the groups the sidebar lists the files in. Callers must
// not change the slice they get.
func (app *app) groups() []review.Group {
	var from []review.Group
	if app.rev.Overview != nil {
		from = app.rev.Overview.Groups
	}
	if cache := &app.grouped; sameSlice(cache.from, from) && sameSlice(cache.files, app.files) {
		return cache.groups
	}
	var groups []review.Group
	if len(from) > 0 {
		groups = review.NormalizeGroups(from, app.fileNames())
	} else {
		groups = review.DirectoryGroups(app.fileNames())
	}
	app.grouped = groupsCache{groups: groups, ids: groupIDs(groups), from: from, files: app.files}
	return groups
}

// groupIDs are the ids of a group page, its sidebar item and its open
// state, in the order of groups. The id is the title, so that a page
// keeps its group when the groups change. A title that an earlier group
// has already taken gets a number, such as "Tests #2".
func groupIDs(groups []review.Group) []string {
	ids := make([]string, len(groups))
	taken := map[string]bool{}
	for i, g := range groups {
		id := g.Title
		for n := 2; taken[id]; n++ {
			id = fmt.Sprintf("%s #%d", g.Title, n)
		}
		taken[id] = true
		ids[i] = id
	}
	return ids
}

// groupID is the id of the group at index i of groups().
func (app *app) groupID(index int) string {
	app.groups()
	return app.grouped.ids[index]
}

// groupsCache holds the groups made from the groups of an overview and
// the changed files. Every change of the files or the overview groups
// puts a new slice in place, so the same slices give the same groups.
// The cache keeps the slices it was made from, so a new slice cannot
// get the address of an old one. The zero cache holds the groups of no
// files: none.
type groupsCache struct {
	groups []review.Group
	ids    []string
	from   []review.Group
	files  []gitx.FileChange
}

// sameSlice reports whether x and y are the same elements of the same
// array.
func sameSlice[Element any](first, second []Element) bool {
	return len(first) == len(second) && (len(first) == 0 || &first[0] == &second[0])
}

// startOver forgets the review of the current scope: its overview,
// threads and viewed files.
func (app *app) startOver() {
	rev, err := app.reviews.StartOver(app.repo.Root, app.scope)
	if err != nil {
		app.showError("Could not start the review over", err)
		return
	}
	app.release(app.rev)
	app.leaveReview()
	app.rev = rev
	app.drafts = map[string]string{}
}

func (app *app) confirmStartOver() {
	app.confirm("Start this review over?", "The overview, the deeper walkthroughs, all comments and threads, and the viewed marks of this scope are deleted.", "Start Over", app.startOver)
}

// confirm runs fn after the user agrees in a warning dialog. A test can
// set askConfirm in place of the dialog. Without both, fn runs at once.
func (app *app) confirm(message, detail, button string, fn func()) {
	if app.askConfirm != nil {
		app.askConfirm(message, detail, button, fn)
		return
	}
	if app.win == nil {
		fn()
		return
	}
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: app.win, Type: mygo.MessageWarning,
			Message: message, Detail: detail,
			Buttons: []string{button, "Cancel"},
		})
		if err == nil && res.Button == 0 {
			app.update(fn)
		}
	}()
}

// showError tells the user that an action failed, in an error dialog.
// Without a window, in tests, it logs the error.
func (app *app) showError(message string, err error) {
	if app.win == nil {
		log.Printf("%s: %v", message, err)
		return
	}
	go func() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Parent: app.win, Type: mygo.MessageError,
			Message: message, Detail: err.Error(),
		})
	}()
}

func (app *app) saveReview() {
	if app.rev != nil {
		app.saveReviewOf(app.rev)
	}
}

// saveReviewOf saves r while it is the review of its scope. A review that
// was started over or replaced is obsolete, and a late agent answer must
// not write it back.
func (app *app) saveReviewOf(rev *review.Review) {
	if app.reviews.Obsolete(rev) {
		return
	}
	if rev == app.rev {
		app.keepSnippets()
	}
	app.saved(app.store.SaveReview(rev))
}

func (app *app) saveConfig() {
	app.saved(app.store.SaveConfig(app.cfg))
}

// saved keeps the error of a write for the notice, or clears the notice
// when the write worked.
func (app *app) saved(err error) {
	app.saveErr = errText(err)
}

func (app *app) scopeLabel() string {
	if app.scope.Kind == gitx.ScopeCommit {
		for _, c := range app.commits.rows {
			if c.SHA == app.scope.Commit {
				return fmt.Sprintf("%s %s", c.Short, c.Subject)
			}
		}
	}
	return app.scope.Label()
}

func (app *app) openThread(id string) {
	app.closeEmptyThread(id)
	app.panel = panelThread(id)
}

// closeEmptyThread drops a thread the user opened and left without
// writing anything, unless it is the one being opened. It reports whether
// it dropped one.
func (app *app) closeEmptyThread(keep string) bool {
	id := app.threadID()
	if id == "" || id == keep || app.rev == nil {
		return false
	}
	thread := app.rev.Thread(id)
	if thread == nil || len(thread.Messages) > 0 {
		return false
	}
	app.rev.DeleteThread(thread.ID)
	delete(app.drafts, thread.ID)
	return true
}

// leaveReview resets perReview before another review takes the place of
// the one the window shows. It reports whether it dropped an empty thread.
// Then the caller saves the review.
func (app *app) leaveReview() bool {
	dropped := app.closeEmptyThread("")
	app.perReview = newPerReview()
	return dropped
}

func (app *app) newThread(anchor review.Anchor) {
	thread := app.rev.AddThread(anchor)
	app.openThread(thread.ID)
}

// quoteLines returns lines start..end (1-based) of a side of a file.
func (app *app) quoteLines(path string, side review.Side, start, end int) string {
	diff := app.diffs[path]
	if diff == nil || diff.state == nil {
		return ""
	}
	buf := diff.state.New
	if side == review.SideOld {
		buf = diff.state.Old
	}
	var lines []string
	for i := start - 1; i < end && i < buf.LineCount(); i++ {
		if i >= 0 {
			lines = append(lines, buf.Line(i))
		}
	}
	return strings.Join(lines, "\n")
}

func sideOf(side editor.DiffSide) review.Side {
	if side == editor.DiffOld {
		return review.SideOld
	}
	return review.SideNew
}

func diffSide(side review.Side) editor.DiffSide {
	if side == review.SideOld {
		return editor.DiffOld
	}
	return editor.DiffNew
}
