package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// menu is the menu bar. Its shortcuts work wherever the keyboard focus
// is, and the menus show them.
func (a *app) menu() *mygo.Menu {
	item := func(label, key string, fn func()) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Accelerator: key, Click: func(*mygo.MenuItem, *mygo.Window) { a.command(fn) }}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "Open Repository…", Accelerator: "CmdOrCtrl+O", Click: func(*mygo.MenuItem, *mygo.Window) { a.chooseRepo() }},
			item("Switch Project…", "CmdOrCtrl+P", a.toggleProjects),
			item("Switch Branch…", "CmdOrCtrl+B", a.toggleBranches),
			item("Compare…", "CmdOrCtrl+Shift+B", a.openCompare),
			item("Refresh", "CmdOrCtrl+R", a.refresh),
			mygo.Separator(),
			item("Settings…", "CmdOrCtrl+,", a.openSettings),
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "View", Submenu: []*mygo.MenuItem{
			item("Agent Log", "CmdOrCtrl+L", a.toggleLog),
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Label: "Go", Submenu: []*mygo.MenuItem{
			item("Back", "CmdOrCtrl+[", a.goBack),
			item("Forward", "CmdOrCtrl+]", a.goForward),
			mygo.Separator(),
			item("Overview", "CmdOrCtrl+1", func() { a.goTo("overview") }),
			item("Questions & Comments", "CmdOrCtrl+2", func() { a.goTo("threads") }),
			item("Feedback", "CmdOrCtrl+3", func() { a.goTo("feedback") }),
			mygo.Separator(),
			item("Next File", "Alt+CmdOrCtrl+Down", func() { a.stepFile(1) }),
			item("Previous File", "Alt+CmdOrCtrl+Up", func() { a.stepFile(-1) }),
		}},
		{Role: mygo.RoleWindowMenu},
	})
}

// command runs a menu command on the open repository and draws the
// window again.
func (a *app) command(fn func()) {
	if a.repo == nil {
		return
	}
	fn()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// keys handles the keys that stay out of the menu: Escape, which dialogs
// and text fields also use, and the back and forward buttons of a mouse.
func (a *app) keys(c *ui.Context) {
	if c.Shortcut(0, ui.KeyEscape) {
		a.escape()
	}
	if c.Shortcut(0, ui.KeyBack) {
		a.goBack()
	}
	if c.Shortcut(0, ui.KeyForward) {
		a.goForward()
	}
}

// escape closes the side panel on top: a thread or question, then the
// agent log.
func (a *app) escape() {
	switch {
	case a.thread != "":
		a.closeThread()
	case a.questionOpen():
		a.closeQuestion()
	case a.logOpen:
		a.logOpen = false
	}
}

func (a *app) toggleLog() {
	if a.logOpen && a.logTarget == "" {
		a.logOpen = false
		return
	}
	a.showLog("")
}

// stepFile opens the next or the previous file in review order, or the
// first one from a page that shows no file.
func (a *app) stepFile(delta int) {
	files := a.fileOrder()
	cur, ok := strings.CutPrefix(a.page, "file:")
	i := -1
	if ok {
		i = slices.Index(files, cur)
	}
	if i < 0 {
		// From before the first file, the next one is the first listed.
		delta = 1
	}
	// The open file can be hidden, so the step starts from its place in
	// the full order and skips the files the sidebar hides.
	for i += delta; i >= 0 && i < len(files); i += delta {
		if a.listed(files[i]) {
			a.goTo("file:" + files[i])
			return
		}
	}
}
