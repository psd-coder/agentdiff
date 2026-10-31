package app

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// command is an action of the app. The menu bar, the command palette and
// the buttons of the window all run it, so it works and shows the same
// way in each.
type command struct {
	id    string
	title string // in the menu bar and the palette
	keys  string // the menu's accelerator, as "CmdOrCtrl+P"
	tip   string // what it does, for the tooltip of its button
	// anywhere runs it without an open repository.
	anywhere bool
	// overlay is set on a command that opens a dialog or a list over the
	// window, which closes the others.
	overlay bool
	// target is what the command acts on from the menu bar or the
	// palette: the file of the page, the open thread. ok is false where
	// there is none. A nil target acts on the app as a whole.
	target func() (target string, ok bool)
	// can tells whether the command applies to target. Nil: it does.
	can func(target string) bool
	run func(c *ui.Context, target string)
}

// commands lists the app's commands, in the order the palette shows them.
func (a *app) commands() []*command {
	return []*command{
		// Actions on the side panel, the page and the scope.
		{id: "comment-lines", title: "Comment on Selected Lines", target: a.targetFile, can: func(f string) bool {
			_, ok := a.selectedLines(f)
			return ok
		}, run: func(_ *ui.Context, f string) {
			r, _ := a.selectedLines(f)
			a.lineThread(f, r)
			a.diffs[f].state.SetSelectedLines(editor.DiffLineRange{}, false)
		}},
		{id: "comment-file", title: "Comment on File", target: a.targetFile, run: func(_ *ui.Context, f string) {
			a.newThread(review.Anchor{Kind: review.AnchorFile, File: f})
		}},
		{id: "mark-viewed", title: "Mark File as Viewed", target: a.targetFile,
			can: func(f string) bool { return !a.rev.Viewed[f] },
			run: func(_ *ui.Context, f string) { a.setViewed([]string{f}, true) }},
		{id: "mark-not-viewed", title: "Mark File as Not Viewed", target: a.targetFile,
			can: func(f string) bool { return a.rev.Viewed[f] },
			run: func(_ *ui.Context, f string) { a.setViewed([]string{f}, false) }},
		{id: "mark-group-viewed", title: "Mark All Files of the Group as Viewed", target: a.targetGroup,
			can: func(g string) bool { return !a.groupViewed(g) },
			run: func(_ *ui.Context, g string) { a.setViewed(a.groupFiles(g), true) }},
		{id: "mark-group-not-viewed", title: "Mark All Files of the Group as Not Viewed", target: a.targetGroup,
			can: a.groupViewed,
			run: func(_ *ui.Context, g string) { a.setViewed(a.groupFiles(g), false) }},
		{id: "mark-all-viewed", title: "Mark All Files as Viewed", target: a.targetAll,
			can: func(string) bool { return !a.allViewed() },
			run: func(*ui.Context, string) { a.setViewed(a.fileNames(), true) }},
		{id: "mark-all-not-viewed", title: "Mark All Files as Not Viewed", target: a.targetAll,
			can: func(string) bool { return a.allViewed() },
			run: func(*ui.Context, string) { a.setViewed(a.fileNames(), false) }},

		{id: "close-thread", title: "Close Thread", tip: "Escape", target: a.targetThread, run: func(*ui.Context, string) { a.closeThread() }},
		{id: "resolve-thread", title: "Resolve Thread", target: a.targetThread,
			can: func(id string) bool { return !a.rev.Thread(id).Resolved },
			run: func(_ *ui.Context, id string) { a.setResolved(id, true) }},
		{id: "reopen-thread", title: "Reopen Thread", target: a.targetThread,
			can: func(id string) bool { return a.rev.Thread(id).Resolved },
			run: func(_ *ui.Context, id string) { a.setResolved(id, false) }},
		{id: "include-thread", title: "Include Thread in Feedback", target: a.targetThread,
			can: func(id string) bool { return a.rev.Thread(id).Excluded },
			run: func(_ *ui.Context, id string) { a.setExcluded(id, false) }},
		{id: "exclude-thread", title: "Leave Thread out of Feedback", target: a.targetThread,
			can: func(id string) bool { return !a.rev.Thread(id).Excluded },
			run: func(_ *ui.Context, id string) { a.setExcluded(id, true) }},
		{id: "stop-thread", title: "Stop the Agent in the Thread", target: a.targetThread,
			can: a.threadBusy,
			run: func(_ *ui.Context, id string) { a.stopThread(a.rev.Thread(id)) }},
		{id: "thread-log", title: "Show Log of the Thread", tip: "What the agent did in this thread", target: a.targetThread,
			can: func(id string) bool { return a.rev.Thread(id).Agent != "" },
			run: func(_ *ui.Context, id string) { a.showLog(id) }},
		{id: "delete-thread", title: "Delete Thread", target: a.targetThread, run: func(_ *ui.Context, id string) { a.deleteThread(id) }},

		{id: "close-question", title: "Close Question", tip: "Escape", target: a.targetQuestion, run: func(*ui.Context, string) { a.closeQuestion() }},
		{id: "question-page", title: "Open Question as Page", target: a.targetQuestionWalk, run: func(_ *ui.Context, id string) {
			a.closeQuestion()
			a.goTo("walk:" + id)
		}},
		{id: "question-log", title: "Show Log of the Question", target: a.targetQuestionWalk, run: func(_ *ui.Context, id string) { a.showLog(id) }},
		{id: "delete-question", title: "Delete Question", target: a.targetQuestionWalk, run: func(_ *ui.Context, id string) {
			a.stopWalk(id)
			a.rev.DeleteWalk(id)
			a.closeQuestion()
			a.saveReview()
		}},

		{id: "stop-walk", title: "Stop Walkthrough", target: a.targetWalk, can: a.walkBusy,
			run: func(_ *ui.Context, id string) { a.stopWalk(id) }},
		{id: "regenerate-walk", title: "Regenerate Walkthrough", target: a.targetWalk,
			can: func(id string) bool { return !a.walkBusy(id) },
			run: func(_ *ui.Context, id string) { a.regenerate(id) }},
		{id: "walk-log", title: "Show Log of the Walkthrough", tip: "What the agent did for this walkthrough", target: a.targetWalk,
			run: func(_ *ui.Context, id string) { a.showLog(id) }},
		{id: "delete-walk", title: "Delete Walkthrough", tip: "Delete this walkthrough and the ones under it", target: a.targetWalk,
			run: func(_ *ui.Context, id string) { a.deleteWalk(a.rev.Walk(id)) }},

		{id: "copy-feedback", title: "Copy Feedback", target: a.targetFeedback, can: a.hasFeedback, run: func(c *ui.Context, _ string) {
			c.WriteClipboard(a.feedback())
			c.Toast("Feedback copied")
		}},
		{id: "save-feedback", title: "Save Feedback to File…", target: a.targetFeedback, can: a.hasFeedback,
			run: func(c *ui.Context, _ string) { a.saveFeedback(c.Services()) }},
		{id: "copy-agent-prompt", title: "Copy Prompt for Agent", tip: "Copy a prompt that tells the agent to read the saved feedback", target: a.targetFeedback,
			can: func(string) bool { return a.savedPath != "" },
			run: func(c *ui.Context, _ string) {
				c.WriteClipboard(agentPrompt(a.savedPath))
				c.Toast("Prompt copied")
			}},

		{id: "explain", title: "Explain Changes", can: func(string) bool {
			return a.overviewRunning() == nil && !a.rev.Overview.Done() && len(a.files) > 0
		}, run: func(*ui.Context, string) { a.generateOverview() }},
		{id: "stop-overview", title: "Stop Overview", can: func(string) bool { return a.overviewRunning() != nil },
			run: func(*ui.Context, string) { a.stopOverview() }},
		{id: "regenerate-overview", title: "Regenerate Overview", can: func(string) bool {
			return a.overviewRunning() == nil && a.rev.Overview.Done() && len(a.files) > 0
		}, run: func(*ui.Context, string) { a.regenerate("") }},
		{id: "overview-log", title: "Show Log of the Overview", tip: "What the agent did to write the overview",
			can: func(string) bool { return a.overviewRunning() != nil || a.rev.Overview != nil },
			run: func(*ui.Context, string) { a.showLog(store.TargetOverview) }},
		{id: "start-over", title: "Start Over", tip: "Delete the overview and comments of this scope",
			can: func(string) bool { return a.rev.Overview != nil || len(a.rev.Threads) > 0 || len(a.rev.Walks) > 0 },
			run: func(*ui.Context, string) { a.confirmStartOver() }},

		{id: "open-moved-commit", title: "Open Commit That Holds These Changes",
			can: func(string) bool { return a.movedTo != "" && a.stale() },
			run: func(*ui.Context, string) { a.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: a.movedTo}) }},
		{id: "open-archived-commit", title: "Open Commit of the Earlier Review", target: a.targetArchived,
			can: func(id string) bool { return a.earlierReview(id).movedTo != "" },
			run: func(_ *ui.Context, id string) {
				a.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: a.earlierReview(id).movedTo})
			}},
		{id: "move-archived", title: "Move Earlier Review to the Commit", target: a.targetArchived,
			can: func(id string) bool { e := a.earlierReview(id); return e.movedTo == "" && e.probably != "" },
			run: func(_ *ui.Context, id string) { a.moveArchived(a.earlierReview(id)) }},
		{id: "restore-archived", title: "Restore Earlier Review", tip: "Bring the review back to the uncommitted changes, with a warning that it was written for other changes",
			target: a.targetArchived, can: func(string) bool { return a.rev.Empty() },
			run: func(_ *ui.Context, id string) { a.restoreArchived(a.earlierReview(id)) }},
		{id: "delete-archived", title: "Delete Earlier Review", target: a.targetArchived, run: func(_ *ui.Context, id string) {
			e := a.earlierReview(id)
			a.confirm("Delete the earlier review?", "Its overview, walkthroughs and comments are deleted.", "Delete", func() { a.deleteArchived(e) })
		}},

		// How the window shows the changes.
		{id: "split-diffs", title: "Show Split Diffs", can: func(string) bool { return a.cfg.Unified },
			run: func(*ui.Context, string) { a.setUnified(false) }},
		{id: "unified-diffs", title: "Show Unified Diffs", can: func(string) bool { return !a.cfg.Unified },
			run: func(*ui.Context, string) { a.setUnified(true) }},
		{id: "flat-files", title: "List Files by Path", can: func(string) bool { return !a.cfg.FlatFiles },
			run: func(*ui.Context, string) { a.setFlatFiles(true) }},
		{id: "grouped-files", title: "List Files by Group", can: func(string) bool { return a.cfg.FlatFiles },
			run: func(*ui.Context, string) { a.setFlatFiles(false) }},
		{id: "hide-viewed", title: "Hide Viewed Files", can: func(string) bool { return !a.cfg.HideViewed },
			run: func(*ui.Context, string) { a.setHideViewed(true) }},
		{id: "show-viewed", title: "Show Viewed Files", can: func(string) bool { return a.cfg.HideViewed },
			run: func(*ui.Context, string) { a.setHideViewed(false) }},
		{id: "close-log", title: "Close Agent Log", can: func(string) bool { return a.logOpen },
			run: func(*ui.Context, string) { a.logOpen = false }},
		{id: "log-show-all", title: "Show All Runs in Agent Log", can: func(string) bool { return a.logOpen && a.logTarget != "" },
			run: func(*ui.Context, string) { a.logTarget = "" }},

		// The menu bar's commands.
		{id: "open-repo", title: "Open Repository…", keys: "CmdOrCtrl+O", anywhere: true, run: func(*ui.Context, string) { a.chooseRepo() }},
		{id: "switch-project", overlay: true, title: "Switch Project…", keys: "CmdOrCtrl+P", tip: "Open another project", run: func(*ui.Context, string) { a.toggleProjects() }},
		{id: "switch-branch", overlay: true, title: "Switch Branch…", keys: "CmdOrCtrl+B", tip: "Branch to review", run: func(*ui.Context, string) { a.toggleBranches() }},
		{id: "compare", overlay: true, title: "Compare…", keys: "CmdOrCtrl+Shift+B", tip: "Compare with another base, or review a custom range",
			run: func(*ui.Context, string) { a.openCompare() }},
		{id: "refresh", title: "Refresh", keys: "CmdOrCtrl+R", tip: "Read the changes again", run: func(*ui.Context, string) { a.refresh() }},
		{id: "settings", overlay: true, title: "Settings…", keys: "CmdOrCtrl+,", run: func(*ui.Context, string) { a.openSettings() }},
		{id: "focus-changes", title: "Focus Changes", keys: "CmdOrCtrl+Shift+C", run: func(*ui.Context, string) { a.refocus = "changes" }},
		{id: "focus-files", title: "Focus Files", keys: "CmdOrCtrl+Shift+F", run: func(*ui.Context, string) { a.focusFiles() }},
		{id: "focus-page", title: "Focus Page", keys: "CmdOrCtrl+0", run: func(*ui.Context, string) { a.refocus = "page" }},
		{id: "palette", overlay: true, title: "Command Palette…", keys: "CmdOrCtrl+Shift+P", run: func(*ui.Context, string) { a.openPalette() }},
		{id: "agent-log", title: "Agent Log", keys: "CmdOrCtrl+L", tip: "Every call to the agent: command, prompt, tool calls, tokens",
			run: func(*ui.Context, string) { a.toggleLog() }},
		{id: "back", title: "Back", keys: "CmdOrCtrl+[", run: func(*ui.Context, string) { a.goBack() }},
		{id: "forward", title: "Forward", keys: "CmdOrCtrl+]", run: func(*ui.Context, string) { a.goForward() }},
		{id: "go-overview", title: "Overview", keys: "CmdOrCtrl+1", run: func(*ui.Context, string) { a.openPage("overview") }},
		{id: "go-threads", title: "Questions & Comments", keys: "CmdOrCtrl+2", run: func(*ui.Context, string) { a.openPage("threads") }},
		{id: "go-feedback", title: "Feedback", keys: "CmdOrCtrl+3", run: func(*ui.Context, string) { a.openPage("feedback") }},
		{id: "go-all", title: "All Files Diff", keys: "CmdOrCtrl+4", run: func(*ui.Context, string) { a.openPage("all") }},
		{id: "next-file", title: "Next File", keys: "Alt+CmdOrCtrl+Down", run: func(*ui.Context, string) { a.stepFile(1) }},
		{id: "previous-file", title: "Previous File", keys: "Alt+CmdOrCtrl+Up", run: func(*ui.Context, string) { a.stepFile(-1) }},
		{id: "shortcuts", overlay: true, title: "Keyboard Shortcuts", keys: "CmdOrCtrl+/", run: func(*ui.Context, string) { a.shortcutsOpen = true }},
	}
}

// cachedCommands is commands, built once: a command reads the app's state
// only in its functions, so the list never changes.
func (a *app) cachedCommands() []*command {
	if a.cmdList == nil {
		a.cmdList = a.commands()
		a.cmds = map[string]*command{}
		for _, cm := range a.cmdList {
			a.cmds[cm.id] = cm
		}
	}
	return a.cmdList
}

// cmd is the command id. It panics on an unknown id, which is a bug.
func (a *app) cmd(id string) *command {
	a.cachedCommands()
	cm := a.cmds[id]
	if cm == nil {
		panic("unknown command " + id)
	}
	return cm
}

// applies tells whether the command acts on target now.
func (a *app) applies(cm *command, target string) bool {
	if a.repo == nil && !cm.anywhere {
		return false
	}
	return cm.can == nil || cm.can(target)
}

// here is the target of the command from the menu bar or the palette, and
// whether the command applies there.
func (a *app) here(cm *command) (string, bool) {
	if a.repo == nil && !cm.anywhere {
		return "", false
	}
	target := ""
	if cm.target != nil {
		var ok bool
		if target, ok = cm.target(); !ok {
			return "", false
		}
	}
	return target, a.applies(cm, target)
}

// runHere runs the command on its target from the menu bar or the palette.
func (a *app) runHere(c *ui.Context, id string) {
	cm := a.cmd(id)
	if target, ok := a.here(cm); ok {
		if cm.overlay {
			a.closeOverlays(id)
		} else {
			a.paletteOpen = false
		}
		cm.run(c, target)
		if cm.overlay {
			// The dialog or list it opened takes the keyboard focus.
			a.refocus = id
		}
	}
}

// closeOverlays closes the dialogs and lists over the window, but the one
// of the command keep, which toggles its own.
func (a *app) closeOverlays(keep string) {
	if keep != "palette" {
		a.paletteOpen = false
	}
	if keep != "switch-project" {
		a.projectsOpen = false
	}
	if keep != "switch-branch" {
		a.branchesOpen = false
	}
	if keep != "shortcuts" {
		a.shortcutsOpen = false
	}
	if f := a.compareForm; f != nil && keep != "compare" {
		f.open = false
	}
	if f := a.settings; f != nil && keep != "settings" {
		f.open = false
		a.recording = ""
	}
}

// queueCommand runs the command in the next frame, which has the context that
// some commands need, and draws the window again. The menu bar runs its
// commands with it.
func (a *app) queueCommand(id string) {
	if a.recording != "" {
		// The menu bar takes its keys before the window sees them: they
		// are the keys being recorded.
		a.recordKeys(a.keysOf(a.cmd(id)))
		return
	}
	a.pending = id
	if a.win != nil {
		a.win.Invalidate()
	}
}

// runQueued runs the command the menu bar queued.
func (a *app) runQueued(c *ui.Context) {
	if id := a.pending; id != "" {
		a.pending = ""
		a.runHere(c, id)
	}
}

// tooltip is the tooltip of the command's button: what it does and its
// keys.
func (a *app) tooltip(cm *command) string {
	keys := a.keysOf(cm)
	switch {
	case keys != "" && cm.tip != "":
		return cm.tip + " (" + keyLabel(keys) + ")"
	case keys != "":
		return keyLabel(keys)
	}
	return cm.tip
}

// runOn runs the command on target from a button of the window.
func (a *app) runOn(c *ui.Context, id, target string) {
	if cm := a.cmd(id); a.applies(cm, target) {
		cm.run(c, target)
	}
}

// cmdButton shows the command as a button acting on target, labeled
// label. It shows nothing where the command does not apply.
func (a *app) cmdButton(c *ui.Context, id, target, label string) {
	a.cmdButtonBase(c, id, target, label, ui.Button)
}

// cmdPrimaryButton is cmdButton with the look of the main action.
func (a *app) cmdPrimaryButton(c *ui.Context, id, target, label string) {
	a.cmdButtonBase(c, id, target, label, ui.PrimaryButton)
}

func (a *app) cmdButtonBase(c *ui.Context, id, target, label string, button func(*ui.Context, string) ui.Element) {
	cm := a.cmd(id)
	if !a.applies(cm, target) {
		return
	}
	b := button(c.Key(id+target), label).OnClick(func() { cm.run(c, target) })
	if tip := a.tooltip(cm); tip != "" {
		b.Tooltip(tip)
	}
}

// cmdCheckbox shows the commands on and off, a pair of which one applies
// at a time, as a checkbox on target: checked while off applies.
func (a *app) cmdCheckbox(c *ui.Context, on, off, target, label string) ui.Element {
	checked := a.applies(a.cmd(off), target)
	box := ui.Checkbox(c.Key(on+target), &checked, label)
	if box.Changed() {
		id := off
		if checked {
			id = on
		}
		a.cmd(id).run(c, target)
	}
	return box
}

// cmdSegmented shows commands, of which all but the one in effect apply,
// as a segmented control.
func (a *app) cmdSegmented(c *ui.Context, ids []string, labels ...string) ui.Element {
	at := 0
	for i, id := range ids {
		if !a.applies(a.cmd(id), "") {
			at = i
		}
	}
	seg := ui.Segmented(c, &at, labels...)
	if seg.Changed() {
		a.cmd(ids[at]).run(c, "")
	}
	return seg
}

// focusFiles moves the keyboard focus to the list of files, on the file
// or group open, or else on the first file listed, which it opens.
func (a *app) focusFiles() {
	a.refocus = "files"
	if f, ok := a.targetFile(); ok && a.listed(f) {
		return
	}
	if g, ok := a.targetGroup(); ok && !a.cfg.FlatFiles && slices.ContainsFunc(a.groupFiles(g), a.listed) {
		return
	}
	for _, f := range a.fileOrder() {
		if a.listed(f) {
			a.goTo("file:" + f)
			return
		}
	}
}

func (a *app) targetFile() (string, bool) {
	f, ok := strings.CutPrefix(a.page, "file:")
	return f, ok && a.diffs[f] != nil
}

func (a *app) targetGroup() (string, bool) {
	g, ok := strings.CutPrefix(a.page, "group:")
	return g, ok && a.groupFiles(g) != nil
}

func (a *app) targetWalk() (string, bool) {
	id, ok := strings.CutPrefix(a.page, "walk:")
	return id, ok && a.rev.Walk(id) != nil
}

func (a *app) targetThread() (string, bool) {
	return a.thread, a.thread != "" && a.rev.Thread(a.thread) != nil
}

func (a *app) targetQuestion() (string, bool) { return "", a.thread == "" && a.questionOpen() }

func (a *app) targetQuestionWalk() (string, bool) {
	_, open := a.targetQuestion()
	return a.question, open && a.rev.Walk(a.question) != nil
}

func (a *app) targetFeedback() (string, bool) { return "", a.page == "feedback" }

func (a *app) targetAll() (string, bool) { return "", a.page == "all" && len(a.files) > 0 }

func (a *app) targetArchived() (string, bool) {
	id, ok := strings.CutPrefix(a.page, "archived:")
	return id, ok && a.earlierReview(id) != nil
}

func (a *app) hasFeedback(string) bool {
	return len(review.ExportedThreads(a.rev, a.exportOptions())) > 0
}

// groupIndex finds the group with the id g, the part of a group page
// name after "group:", and reports whether the group exists.
func (a *app) groupIndex(g string) (int, bool) {
	a.groups()
	i := slices.Index(a.grouped.ids, g)
	return i, i >= 0
}

// groupFiles are the files of the group with the id g, nil for none.
func (a *app) groupFiles(g string) []string {
	i, ok := a.groupIndex(g)
	if !ok {
		return nil
	}
	return a.groups()[i].Files
}

func (a *app) groupViewed(g string) bool {
	for _, f := range a.groupFiles(g) {
		if !a.rev.Viewed[f] {
			return false
		}
	}
	return true
}

func (a *app) allViewed() bool {
	for _, f := range a.files {
		if !a.rev.Viewed[f.Path] {
			return false
		}
	}
	return true
}

func (a *app) selectedLines(f string) (editor.DiffLineRange, bool) {
	d := a.diffs[f]
	if d == nil || d.state == nil {
		return editor.DiffLineRange{}, false
	}
	return d.state.SelectedLines()
}

func (a *app) setResolved(id string, resolved bool) {
	a.rev.Thread(id).Resolved = resolved
	a.saveReview()
}

func (a *app) setExcluded(id string, excluded bool) {
	a.rev.Thread(id).Excluded = excluded
	a.saveReview()
}

func (a *app) deleteThread(id string) {
	a.stopThread(a.rev.Thread(id))
	a.rev.DeleteThread(id)
	a.thread = ""
	a.saveReview()
}

func (a *app) setUnified(on bool) {
	a.cfg.Unified = on
	a.saveConfig()
}

func (a *app) setFlatFiles(on bool) {
	a.cfg.FlatFiles = on
	a.saveConfig()
}

func (a *app) setHideViewed(on bool) {
	a.cfg.HideViewed = on
	a.saveConfig()
}
