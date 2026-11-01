package app

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/store"
)

// Run opens the window and runs the app until it quits. It opens the
// repository at repo, or the welcome screen when repo is empty.
func Run(repo string) error {
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
			// The traffic lights sit over the sidebar, and the toolbar
			// shares the title bar.
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: titleBarHeight,
			// macOS keeps its own title bar height for the traffic lights;
			// this centers the 14 points of their height in the toolbar.
			TrafficLightPosition: &mygo.Point{X: 20, Y: (titleBarHeight - 14) / 2},
			// The sidebar draws no background, and the material shows.
			Vibrancy: mygo.VibrancySidebar,
			Content:  ui.View(app.view),
		})
		app.win.OnFocus(func() { app.win.Update(app.syncBranch) })
		if repo != "" {
			app.win.Update(func() { app.openRepo(repo) })
		}
	})
	return mygo.App.Run()
}
