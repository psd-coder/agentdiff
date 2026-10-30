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
		a := newApp(store.Store{Dir: dir})
		a.watchFiles = watchFiles
		a.applyTheme()
		mygo.App.SetMenu(a.menu())
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "AgentDiff",
			Width:     1400,
			Height:    900,
			MinWidth:  900,
			MinHeight: 560,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		a.win.OnFocus(func() { a.win.Update(a.syncBranch) })
		if repo != "" {
			a.win.Update(func() { a.openRepo(repo) })
		}
	})
	return mygo.App.Run()
}
