package app

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// menu is the menu bar. Its shortcuts work wherever the keyboard focus
// is, and the menus show them.
func (app *app) menu() *mygo.Menu { return app.menuBar(app.keymap().of) }

// menuBar is the menu bar with the keys of each command from keys.
func (app *app) menuBar(keys func(id string) string) *mygo.Menu {
	item := func(id string) *mygo.MenuItem {
		cm := app.cmd(id)
		return &mygo.MenuItem{ID: id, Label: cm.title, Accelerator: keys(id), Click: func(*mygo.MenuItem, *mygo.Window) { app.queueCommand(id) }}
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
func (app *app) menuIDs() map[string]bool {
	if app.inMenu != nil {
		return app.inMenu
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
	walk(app.menu().Items())
	app.inMenu = ids
	return ids
}

// keys handles the keys that stay out of the menu: Escape, which dialogs
// and text fields also use, and the back and forward buttons of a mouse.
func (app *app) keys(ctx *ui.Context) {
	app.commandKeys(ctx)
	if ctx.Shortcut(0, ui.KeyEscape) {
		app.escape()
	}
	if ctx.Shortcut(0, ui.KeyBack) {
		app.goBack()
	}
	if ctx.Shortcut(0, ui.KeyForward) {
		app.goForward()
	}
}

// escape closes the side panel on top: a thread or question, then the
// agent log.
func (app *app) escape() {
	switch {
	case app.threadID() != "":
		app.closeThread()
	case app.questionOpen():
		app.closeQuestion()
	case app.logOpen:
		app.logOpen = false
	}
}

func (app *app) toggleLog() {
	if app.logOpen && app.logTarget == "" {
		app.logOpen = false
		return
	}
	app.showLog("")
}

// stepFile opens the next or the previous file in review order, or the
// first one from a page that shows no file.
func (app *app) stepFile(delta int) {
	files := app.fileOrder()
	index := -1
	if openPage := app.nav.page; openPage.kind == kindFile {
		index = slices.Index(files, openPage.key)
	}
	if index < 0 {
		// From before the first file, the next one is the first listed.
		delta = 1
	}
	// The open file can be hidden, so the step starts from its place in
	// the full order and skips the files the sidebar hides.
	for index += delta; index >= 0 && index < len(files); index += delta {
		if app.listed(files[index]) {
			app.nav.goTo(page{kindFile, files[index]})
			return
		}
	}
}
