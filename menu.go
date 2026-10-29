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
	item := func(id string) *mygo.MenuItem {
		cm := a.cmd(id)
		return &mygo.MenuItem{ID: id, Label: cm.title, Accelerator: a.keysOf(cm), Click: func(*mygo.MenuItem, *mygo.Window) { a.queueCommand(id) }}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			item("open-repo"),
			item("switch-project"),
			item("switch-branch"),
			item("compare"),
			item("refresh"),
			mygo.Separator(),
			item("settings"),
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "View", Submenu: []*mygo.MenuItem{
			item("palette"),
			mygo.Separator(),
			item("focus-changes"),
			item("focus-files"),
			item("focus-page"),
			mygo.Separator(),
			item("agent-log"),
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Label: "Go", Submenu: []*mygo.MenuItem{
			item("back"),
			item("forward"),
			mygo.Separator(),
			item("go-overview"),
			item("go-threads"),
			item("go-feedback"),
			item("go-all"),
			mygo.Separator(),
			item("next-file"),
			item("previous-file"),
		}},
		{Role: mygo.RoleWindowMenu},
		{Role: mygo.RoleHelp, Submenu: []*mygo.MenuItem{
			item("shortcuts"),
		}},
	})
}

// menuIDs are the commands the menu bar holds.
func (a *app) menuIDs() map[string]bool {
	if a.inMenu != nil {
		return a.inMenu
	}
	ids := map[string]bool{}
	var walk func(items []*mygo.MenuItem)
	walk = func(items []*mygo.MenuItem) {
		for _, it := range items {
			if it.ID != "" {
				ids[it.ID] = true
			}
			walk(it.Submenu)
		}
	}
	walk(a.menu().Items())
	a.inMenu = ids
	return ids
}

// keys handles the keys that stay out of the menu: Escape, which dialogs
// and text fields also use, and the back and forward buttons of a mouse.
func (a *app) keys(c *ui.Context) {
	a.commandKeys(c)
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
