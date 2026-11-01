package app

import (
	"slices"

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
	run func(ctx *ui.Context, target string)
}

// commands lists the app's commands, in the order the palette shows them.
func (app *app) commands() []*command {
	return []*command{
		// Actions on the side panel, the page and the scope.
		{id: "comment-lines", title: "Comment on Selected Lines", target: app.targetFile, can: func(f string) bool {
			_, ok := app.selectedLines(f)
			return ok
		}, run: func(_ *ui.Context, f string) {
			lines, _ := app.selectedLines(f)
			app.lineThread(f, lines)
			app.diffs[f].state.SetSelectedLines(editor.DiffLineRange{}, false)
		}},
		{id: "comment-file", title: "Comment on File", target: app.targetFile, run: func(_ *ui.Context, f string) {
			app.newThread(review.Anchor{Kind: review.AnchorFile, File: f})
		}},
		{id: "mark-viewed", title: "Mark File as Viewed", target: app.targetFile,
			can: func(f string) bool { return !app.rev.Viewed[f] },
			run: func(_ *ui.Context, f string) { app.setViewed([]string{f}, true) }},
		{id: "mark-not-viewed", title: "Mark File as Not Viewed", target: app.targetFile,
			can: func(f string) bool { return app.rev.Viewed[f] },
			run: func(_ *ui.Context, f string) { app.setViewed([]string{f}, false) }},
		{id: "mark-group-viewed", title: "Mark All Files of the Group as Viewed", target: app.targetGroup,
			can: func(g string) bool { return !app.groupViewed(g) },
			run: func(_ *ui.Context, g string) { app.setViewed(app.groupFiles(g), true) }},
		{id: "mark-group-not-viewed", title: "Mark All Files of the Group as Not Viewed", target: app.targetGroup,
			can: app.groupViewed,
			run: func(_ *ui.Context, g string) { app.setViewed(app.groupFiles(g), false) }},
		{id: "mark-all-viewed", title: "Mark All Files as Viewed", target: app.targetAll,
			can: func(string) bool { return !app.allViewed() },
			run: func(*ui.Context, string) { app.setViewed(app.fileNames(), true) }},
		{id: "mark-all-not-viewed", title: "Mark All Files as Not Viewed", target: app.targetAll,
			can: func(string) bool { return app.allViewed() },
			run: func(*ui.Context, string) { app.setViewed(app.fileNames(), false) }},

		{id: "close-thread", title: "Close Thread", tip: "Escape", target: app.targetThread, run: func(*ui.Context, string) { app.closeThread() }},
		{id: "resolve-thread", title: "Resolve Thread", target: app.targetThread,
			can: func(id string) bool { return !app.rev.Thread(id).Resolved },
			run: func(_ *ui.Context, id string) { app.setResolved(id, true) }},
		{id: "reopen-thread", title: "Reopen Thread", target: app.targetThread,
			can: func(id string) bool { return app.rev.Thread(id).Resolved },
			run: func(_ *ui.Context, id string) { app.setResolved(id, false) }},
		{id: "include-thread", title: "Include Thread in Feedback", target: app.targetThread,
			can: func(id string) bool { return app.rev.Thread(id).Excluded },
			run: func(_ *ui.Context, id string) { app.setExcluded(id, false) }},
		{id: "exclude-thread", title: "Leave Thread out of Feedback", target: app.targetThread,
			can: func(id string) bool { return !app.rev.Thread(id).Excluded },
			run: func(_ *ui.Context, id string) { app.setExcluded(id, true) }},
		{id: "stop-thread", title: "Stop the Agent in the Thread", target: app.targetThread,
			can: app.busy,
			run: func(_ *ui.Context, id string) { app.runner.stop(app.rev, id) }},
		{id: "thread-log", title: "Show Log of the Thread", tip: "What the agent did in this thread", target: app.targetThread,
			can: func(id string) bool { return app.rev.Thread(id).Agent != "" },
			run: func(_ *ui.Context, id string) { app.showLog(id) }},
		{id: "delete-thread", title: "Delete Thread", target: app.targetThread, run: func(_ *ui.Context, id string) { app.deleteThread(id) }},

		{id: "close-question", title: "Close Question", tip: "Escape", target: app.targetQuestion, run: func(*ui.Context, string) { app.closeQuestion() }},
		{id: "question-page", title: "Open Question as Page", target: app.targetQuestionWalk, run: func(_ *ui.Context, id string) {
			app.closeQuestion()
			app.nav.goTo(page{kindWalk, id})
		}},
		{id: "question-log", title: "Show Log of the Question", target: app.targetQuestionWalk, run: func(_ *ui.Context, id string) { app.showLog(id) }},
		{id: "delete-question", title: "Delete Question", target: app.targetQuestionWalk, run: func(_ *ui.Context, id string) {
			app.runner.stop(app.rev, id)
			app.rev.DeleteWalk(id)
			app.closeQuestion()
			app.leaveGone()
			app.saveReview()
		}},

		{id: "stop-walk", title: "Stop Walkthrough", target: app.targetWalk, can: app.busy,
			run: func(_ *ui.Context, id string) { app.runner.stop(app.rev, id) }},
		{id: "regenerate-walk", title: "Regenerate Walkthrough", target: app.targetWalk,
			can: func(id string) bool { return !app.busy(id) },
			run: func(_ *ui.Context, id string) { app.regenerate(id) }},
		{id: "walk-log", title: "Show Log of the Walkthrough", tip: "What the agent did for this walkthrough", target: app.targetWalk,
			run: func(_ *ui.Context, id string) { app.showLog(id) }},
		{id: "delete-walk", title: "Delete Walkthrough", tip: "Delete this walkthrough and the ones under it", target: app.targetWalk,
			run: func(_ *ui.Context, id string) { app.deleteWalk(app.rev.Walk(id)) }},

		{id: "copy-feedback", title: "Copy Feedback", target: app.targetFeedback, can: app.hasFeedback, run: func(c *ui.Context, _ string) {
			c.WriteClipboard(app.feedback())
			c.Toast("Feedback copied")
		}},
		{id: "save-feedback", title: "Save Feedback to File…", target: app.targetFeedback, can: app.hasFeedback,
			run: func(c *ui.Context, _ string) { app.saveFeedback(c.Services()) }},
		{id: "copy-agent-prompt", title: "Copy Prompt for Agent", tip: "Copy a prompt that tells the agent to read the saved feedback", target: app.targetFeedback,
			can: func(string) bool { return app.savedPath != "" },
			run: func(c *ui.Context, _ string) {
				c.WriteClipboard(agentPrompt(app.savedPath))
				c.Toast("Prompt copied")
			}},

		{id: "explain", title: "Explain Changes", can: func(string) bool {
			return !app.busy(store.TargetOverview) && !app.rev.Overview.Done() && len(app.files) > 0
		}, run: func(*ui.Context, string) { app.generateOverview() }},
		{id: "stop-overview", title: "Stop Overview", can: func(string) bool { return app.busy(store.TargetOverview) },
			run: func(*ui.Context, string) { app.runner.stop(app.rev, store.TargetOverview) }},
		{id: "regenerate-overview", title: "Regenerate Overview", can: func(string) bool {
			return !app.busy(store.TargetOverview) && app.rev.Overview.Done() && len(app.files) > 0
		}, run: func(*ui.Context, string) { app.regenerate("") }},
		{id: "overview-log", title: "Show Log of the Overview", tip: "What the agent did to write the overview",
			can: func(string) bool { return app.busy(store.TargetOverview) || app.rev.Overview != nil },
			run: func(*ui.Context, string) { app.showLog(store.TargetOverview) }},
		{id: "start-over", title: "Start Over", tip: "Delete the overview and comments of this scope",
			can: func(string) bool {
				return app.rev.Overview != nil || len(app.rev.Threads) > 0 || len(app.rev.Walks) > 0
			},
			run: func(*ui.Context, string) { app.confirmStartOver() }},

		{id: "open-moved-commit", title: "Open Commit That Holds These Changes",
			can: func(string) bool { return app.movedTo != "" && app.freshness() == overviewStale },
			run: func(*ui.Context, string) { app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: app.movedTo}) }},
		{id: "open-archived-commit", title: "Open Commit of the Earlier Review", target: app.targetArchived,
			can: func(id string) bool { return app.reviews.Find(id).MovedTo != "" },
			run: func(_ *ui.Context, id string) {
				app.setScope(gitx.Scope{Kind: gitx.ScopeCommit, Commit: app.reviews.Find(id).MovedTo})
			}},
		{id: "move-archived", title: "Move Earlier Review to the Commit", target: app.targetArchived,
			can: func(id string) bool {
				earlier := app.reviews.Find(id)
				return earlier.MovedTo == "" && earlier.Probably != ""
			},
			run: func(_ *ui.Context, id string) { app.moveArchived(app.reviews.Find(id)) }},
		{id: "restore-archived", title: "Restore Earlier Review", tip: "Bring the review back to the uncommitted changes, with a warning that it was written for other changes",
			target: app.targetArchived, can: func(string) bool { return app.rev.Empty() },
			run: func(_ *ui.Context, id string) { app.restoreArchived(app.reviews.Find(id)) }},
		{id: "delete-archived", title: "Delete Earlier Review", target: app.targetArchived, run: func(_ *ui.Context, id string) {
			earlier := app.reviews.Find(id)
			app.confirm("Delete the earlier review?", "Its overview, walkthroughs and comments are deleted.", "Delete", func() { app.deleteArchived(earlier) })
		}},

		// How the window shows the changes.
		{id: "split-diffs", title: "Show Split Diffs", can: func(string) bool { return app.cfg.Unified },
			run: func(*ui.Context, string) { app.setUnified(false) }},
		{id: "unified-diffs", title: "Show Unified Diffs", can: func(string) bool { return !app.cfg.Unified },
			run: func(*ui.Context, string) { app.setUnified(true) }},
		{id: "flat-files", title: "List Files by Path", can: func(string) bool { return !app.cfg.FlatFiles },
			run: func(*ui.Context, string) { app.setFlatFiles(true) }},
		{id: "grouped-files", title: "List Files by Group", can: func(string) bool { return app.cfg.FlatFiles },
			run: func(*ui.Context, string) { app.setFlatFiles(false) }},
		{id: "hide-viewed", title: "Hide Viewed Files", can: func(string) bool { return !app.cfg.HideViewed },
			run: func(*ui.Context, string) { app.setHideViewed(true) }},
		{id: "show-viewed", title: "Show Viewed Files", can: func(string) bool { return app.cfg.HideViewed },
			run: func(*ui.Context, string) { app.setHideViewed(false) }},
		{id: "close-log", title: "Close Agent Log", can: func(string) bool { return app.logOpen },
			run: func(*ui.Context, string) { app.logOpen = false }},
		{id: "log-show-all", title: "Show All Runs in Agent Log", can: func(string) bool { return app.logOpen && app.logTarget != "" },
			run: func(*ui.Context, string) { app.logTarget = "" }},

		// The menu bar's commands.
		{id: "open-repo", title: "Open Repository…", keys: "CmdOrCtrl+O", anywhere: true, run: func(*ui.Context, string) { app.chooseRepo() }},
		{id: "switch-project", overlay: true, title: "Switch Project…", keys: "CmdOrCtrl+P", tip: "Open another project", run: func(*ui.Context, string) { app.toggleProjects() }},
		{id: "switch-branch", overlay: true, title: "Switch Branch…", keys: "CmdOrCtrl+B", tip: "Branch to review", run: func(*ui.Context, string) { app.toggleBranches() }},
		{id: "compare", overlay: true, title: "Compare…", keys: "CmdOrCtrl+Shift+B", tip: "Compare with another base, or review a custom range",
			run: func(*ui.Context, string) { app.openCompare() }},
		{id: "refresh", title: "Refresh", keys: "CmdOrCtrl+R", tip: "Read the changes again", run: func(*ui.Context, string) { app.refresh() }},
		{id: "settings", overlay: true, title: "Settings…", keys: "CmdOrCtrl+,", run: func(*ui.Context, string) { app.openSettings() }},
		{id: "focus-changes", title: "Focus Changes", keys: "CmdOrCtrl+Shift+C", run: func(*ui.Context, string) { app.refocus = "changes" }},
		{id: "focus-files", title: "Focus Files", keys: "CmdOrCtrl+Shift+F", run: func(*ui.Context, string) { app.focusFiles() }},
		{id: "focus-page", title: "Focus Page", keys: "CmdOrCtrl+0", run: func(*ui.Context, string) { app.refocus = "page" }},
		{id: "palette", overlay: true, title: "Command Palette…", keys: "CmdOrCtrl+Shift+P", run: func(*ui.Context, string) { app.openPalette() }},
		{id: "agent-log", title: "Agent Log", keys: "CmdOrCtrl+L", tip: "Every call to the agent: command, prompt, tool calls, tokens",
			run: func(*ui.Context, string) { app.toggleLog() }},
		{id: "back", title: "Back", keys: "CmdOrCtrl+[", run: func(*ui.Context, string) { app.goBack() }},
		{id: "forward", title: "Forward", keys: "CmdOrCtrl+]", run: func(*ui.Context, string) { app.goForward() }},
		{id: "go-overview", title: "Overview", keys: "CmdOrCtrl+1", run: func(*ui.Context, string) { app.openPage(overview) }},
		{id: "go-threads", title: "Questions & Comments", keys: "CmdOrCtrl+2", run: func(*ui.Context, string) { app.openPage(page{kind: kindThreads}) }},
		{id: "go-feedback", title: "Feedback", keys: "CmdOrCtrl+3", run: func(*ui.Context, string) { app.openPage(page{kind: kindFeedback}) }},
		{id: "go-all", title: "All Files Diff", keys: "CmdOrCtrl+4", run: func(*ui.Context, string) { app.openPage(page{kind: kindAll}) }},
		{id: "next-file", title: "Next File", keys: "Alt+CmdOrCtrl+Down", run: func(*ui.Context, string) { app.stepFile(1) }},
		{id: "previous-file", title: "Previous File", keys: "Alt+CmdOrCtrl+Up", run: func(*ui.Context, string) { app.stepFile(-1) }},
		{id: "shortcuts", overlay: true, title: "Keyboard Shortcuts", keys: "CmdOrCtrl+/", run: func(*ui.Context, string) { app.shortcutsOpen = true }},
	}
}

// cachedCommands is commands, built once: a command reads the app's state
// only in its functions, so the list never changes.
func (app *app) cachedCommands() []*command {
	if app.cmdList == nil {
		app.cmdList = app.commands()
		app.cmds = map[string]*command{}
		for _, cm := range app.cmdList {
			app.cmds[cm.id] = cm
		}
	}
	return app.cmdList
}

// cmd is the command id. It panics on an unknown id, which is a bug.
func (app *app) cmd(id string) *command {
	app.cachedCommands()
	cm := app.cmds[id]
	if cm == nil {
		panic("unknown command " + id)
	}
	return cm
}

// applies tells whether the command acts on target now.
func (app *app) applies(cm *command, target string) bool {
	if app.repo == nil && !cm.anywhere {
		return false
	}
	return cm.can == nil || cm.can(target)
}

// here is the target of the command from the menu bar or the palette, and
// whether the command applies there.
func (app *app) here(cm *command) (string, bool) {
	if app.repo == nil && !cm.anywhere {
		return "", false
	}
	target := ""
	if cm.target != nil {
		var ok bool
		if target, ok = cm.target(); !ok {
			return "", false
		}
	}
	return target, app.applies(cm, target)
}

// runHere runs the command on its target from the menu bar or the palette.
func (app *app) runHere(ctx *ui.Context, id string) {
	cm := app.cmd(id)
	if target, ok := app.here(cm); ok {
		if cm.overlay {
			app.closeOverlays(id)
		} else {
			app.paletteOpen = false
		}
		cm.run(ctx, target)
		if cm.overlay {
			// The dialog or list it opened takes the keyboard focus.
			app.refocus = id
		}
	}
}

// closeOverlays closes the dialogs and lists over the window, but the one
// of the command keep, which toggles its own.
func (app *app) closeOverlays(keep string) {
	if keep != "palette" {
		app.paletteOpen = false
	}
	if keep != "switch-project" {
		app.projectsOpen = false
	}
	if keep != "switch-branch" {
		app.branchesOpen = false
	}
	if keep != "shortcuts" {
		app.shortcutsOpen = false
	}
	if compare := app.compareForm; compare != nil && keep != "compare" {
		compare.open = false
	}
	if settings := app.settings; settings != nil && keep != "settings" {
		settings.open = false
		app.recording = ""
	}
}

// queueCommand runs the command in the next frame, which has the context that
// some commands need, and draws the window again. The menu bar runs its
// commands with it.
func (app *app) queueCommand(id string) {
	if app.recording != "" {
		// The menu bar takes its keys before the window sees them: they
		// are the keys being recorded.
		app.recordKeys(app.keymap().of(id))
		return
	}
	app.pending = id
	if app.win != nil {
		app.win.Invalidate()
	}
}

// runQueued runs the command the menu bar queued.
func (app *app) runQueued(ctx *ui.Context) {
	if id := app.pending; id != "" {
		app.pending = ""
		app.runHere(ctx, id)
	}
}

// tooltip is the tooltip of the command's button: what it does and its
// keys.
func (app *app) tooltip(cm *command) string {
	keys := app.keymap().of(cm.id)
	switch {
	case keys != "" && cm.tip != "":
		return cm.tip + " (" + keyLabel(keys) + ")"
	case keys != "":
		return keyLabel(keys)
	}
	return cm.tip
}

// runOn runs the command on target from a button of the window.
func (app *app) runOn(ctx *ui.Context, id, target string) {
	if cm := app.cmd(id); app.applies(cm, target) {
		cm.run(ctx, target)
	}
}

// cmdButton shows the command as a button acting on target, labeled
// label. It shows nothing where the command does not apply.
func (app *app) cmdButton(ctx *ui.Context, id, target, label string) {
	app.cmdButtonBase(ctx, id, target, label, ui.Button)
}

// cmdPrimaryButton is cmdButton with the look of the main action.
func (app *app) cmdPrimaryButton(ctx *ui.Context, id, target, label string) {
	app.cmdButtonBase(ctx, id, target, label, ui.PrimaryButton)
}

func (app *app) cmdButtonBase(ctx *ui.Context, id, target, label string, button func(*ui.Context, string) ui.Element) {
	cm := app.cmd(id)
	if !app.applies(cm, target) {
		return
	}
	element := button(ctx.Key(id+target), label).OnClick(func() { cm.run(ctx, target) })
	if tip := app.tooltip(cm); tip != "" {
		element.Tooltip(tip)
	}
}

// cmdCheckbox shows the commands on and off, a pair of which one applies
// at a time, as a checkbox on target: checked while off applies.
func (app *app) cmdCheckbox(ctx *ui.Context, on, off, target, label string) ui.Element {
	checked := app.applies(app.cmd(off), target)
	box := ui.Checkbox(ctx.Key(on+target), &checked, label)
	if box.Changed() {
		id := off
		if checked {
			id = on
		}
		app.cmd(id).run(ctx, target)
	}
	return box
}

// cmdSegmented shows commands, of which all but the one in effect apply,
// as a segmented control.
func (app *app) cmdSegmented(ctx *ui.Context, ids []string, labels ...string) ui.Element {
	at := 0
	for i, id := range ids {
		if !app.applies(app.cmd(id), "") {
			at = i
		}
	}
	seg := ui.Segmented(ctx, &at, labels...)
	if seg.Changed() {
		app.cmd(ids[at]).run(ctx, "")
	}
	return seg
}

// focusFiles moves the keyboard focus to the list of files, on the file
// or group open, or else on the first file listed, which it opens.
func (app *app) focusFiles() {
	app.refocus = "files"
	if file, ok := app.targetFile(); ok && app.listed(file) {
		return
	}
	if groupID, ok := app.targetGroup(); ok && !app.cfg.FlatFiles && slices.ContainsFunc(app.groupFiles(groupID), app.listed) {
		return
	}
	for _, f := range app.fileOrder() {
		if app.listed(f) {
			app.nav.goTo(page{kindFile, f})
			return
		}
	}
}

func (app *app) targetFile() (string, bool) {
	current := app.nav.page
	return current.key, current.kind == kindFile && !app.pageGone(current)
}

func (app *app) targetGroup() (string, bool) {
	current := app.nav.page
	return current.key, current.kind == kindGroup && !app.pageGone(current)
}

func (app *app) targetWalk() (string, bool) {
	current := app.nav.page
	return current.key, current.kind == kindWalk && !app.pageGone(current)
}

func (app *app) targetThread() (string, bool) {
	id := app.threadID()
	return id, id != "" && app.rev.Thread(id) != nil
}

func (app *app) targetQuestion() (string, bool) {
	return "", app.threadID() == "" && app.questionOpen()
}

func (app *app) targetQuestionWalk() (string, bool) {
	_, open := app.targetQuestion()
	id := app.questionID()
	return id, open && app.rev.Walk(id) != nil
}

func (app *app) targetFeedback() (string, bool) { return "", app.nav.page.kind == kindFeedback }

func (app *app) targetAll() (string, bool) {
	return "", app.nav.page.kind == kindAll && len(app.files) > 0
}

func (app *app) targetArchived() (string, bool) {
	current := app.nav.page
	return current.key, current.kind == kindArchived && !app.pageGone(current)
}

func (app *app) hasFeedback(string) bool {
	return len(review.ExportedThreads(app.rev, app.exportOptions())) > 0
}

// groupIndex finds the group with the id g, the part of a group page
// key, and reports whether the group exists.
func (app *app) groupIndex(groupID string) (int, bool) {
	app.groups()
	index := slices.Index(app.grouped.ids, groupID)
	return index, index >= 0
}

// groupFiles are the files of the group with the id g, nil for none.
func (app *app) groupFiles(groupID string) []string {
	index, ok := app.groupIndex(groupID)
	if !ok {
		return nil
	}
	return app.groups()[index].Files
}

func (app *app) groupViewed(groupID string) bool {
	for _, f := range app.groupFiles(groupID) {
		if !app.rev.Viewed[f] {
			return false
		}
	}
	return true
}

func (app *app) allViewed() bool {
	for _, f := range app.files {
		if !app.rev.Viewed[f.Path] {
			return false
		}
	}
	return true
}

func (app *app) selectedLines(file string) (editor.DiffLineRange, bool) {
	diff := app.diffs[file]
	if diff == nil || diff.state == nil {
		return editor.DiffLineRange{}, false
	}
	return diff.state.SelectedLines()
}

func (app *app) setResolved(id string, resolved bool) {
	app.rev.Thread(id).Resolved = resolved
	app.saveReview()
}

func (app *app) setExcluded(id string, excluded bool) {
	app.rev.Thread(id).Excluded = excluded
	app.saveReview()
}

func (app *app) deleteThread(id string) {
	app.runner.stop(app.rev, id)
	app.rev.DeleteThread(id)
	app.panel = nil
	app.saveReview()
}

func (app *app) setUnified(on bool) {
	app.cfg.Unified = on
	app.saveConfig()
}

func (app *app) setFlatFiles(on bool) {
	app.cfg.FlatFiles = on
	app.saveConfig()
}

func (app *app) setHideViewed(on bool) {
	app.cfg.HideViewed = on
	app.saveConfig()
}
