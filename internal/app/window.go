package app

import (
	"log"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/store"
)

// windowState is what the main window shows about the open review: the
// title, the folder the window stands for, and whether the reviewer has
// feedback they did not copy or save yet, which the close button shows
// with a dot, as for a document with unsaved changes. The Dock icon shows
// the number of unseen results as its badge.
type windowState struct {
	title  string
	folder string
	edited bool
	unseen int
}

func (app *app) windowState() windowState {
	unseen := app.targets.count()
	if app.repo == nil {
		return windowState{title: "AgentDiff", unseen: unseen}
	}
	edited := app.hasFeedback("") && app.feedback() != app.handedOver
	return windowState{title: app.repo.Name() + " — " + app.scopeLabel(), folder: app.repo.Root, edited: edited, unseen: unseen}
}

// syncWindow gives the main window the title, folder and edited mark of
// the open review, when they changed.
func (app *app) syncWindow() {
	state := app.windowState()
	if app.win == nil || state == app.shownWindow {
		return
	}
	shown := app.shownWindow
	app.shownWindow = state
	if state.title != shown.title {
		app.win.SetTitle(state.title)
	}
	if state.folder != shown.folder {
		app.win.SetRepresentedFile(state.folder)
	}
	if state.edited != shown.edited {
		app.win.SetDocumentEdited(state.edited)
	}
	if state.unseen != shown.unseen {
		badge := ""
		if state.unseen > 0 {
			badge = strconv.Itoa(state.unseen)
		}
		mygo.App.Dock.SetBadge(badge)
	}
}

// Run opens the window and runs the app until it quits. It opens the
// repository at repo, or the welcome window when repo is empty or does
// not open. icon is the PNG of the app's icon, for the welcome window.
func Run(repo string, icon []byte) error {
	if bitmap, err := ui.DecodeBitmap(icon); err == nil {
		appIcon = bitmap
	}
	// A folder chosen in File > Open Recent or in the Dock menu, or dropped
	// on the Dock icon, opens as the repository. One that comes before the
	// window is open waits for it.
	var opened *app
	mygo.App.OnOpenFile(func(path string) {
		if opened == nil {
			repo = path
			return
		}
		opened.win.Update(func() { opened.openRepo(path) })
	})
	mygo.App.WhenReady(func() {
		dir, err := mygo.App.Path(mygo.PathUserData)
		if err != nil {
			log.Fatal(err)
		}
		app := newApp(store.Store{Dir: dir})
		app.watchFiles = watchFiles
		app.applyTheme()
		mygo.App.SetMenu(app.menu())
		app.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "AgentDiff",
			Width:     1400,
			Height:    900,
			MinWidth:  900,
			MinHeight: 560,
			StateKey:  "main",
			// It shows once a repository opens.
			Hidden: true,
			// On macOS the traffic lights sit over the sidebar, and the
			// toolbar shares the title bar.
			TitleBarStyle:  app.platform.titleBar(mygo.TitleBarHiddenInset),
			TitleBarHeight: titleBarHeight,
			// macOS keeps its own title bar height for the traffic lights;
			// this centers the 14 points of their height in the toolbar.
			TrafficLightPosition: &mygo.Point{X: 20, Y: (titleBarHeight - 14) / 2},
			AutoHideMenuBar:      app.platform.hideMenuBar,
			// The sidebar draws no background, and the material shows.
			Vibrancy: mygo.VibrancySidebar,
			Content:  ui.View(app.view),
		})
		app.win.OnFocus(func() { app.win.Update(app.syncBranch) })
		mygo.App.OnDidResignActive(func() { app.win.Update(func() { app.inactive = true }) })
		mygo.App.OnDidBecomeActive(func() {
			// What the user sees now does not wait in Notification Center.
			mygo.ClearNotifications()
			app.win.Update(func() { app.inactive = false })
		})
		opened = app
		app.win.Update(func() {
			if repo != "" {
				app.openRepo(repo)
			}
			if app.repo == nil {
				app.showWelcome()
			}
		})
	})
	return mygo.App.Run()
}
