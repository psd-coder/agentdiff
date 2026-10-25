package main

import (
	"log"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/store"
)

// startPath is the repository to open: the first argument, or the working
// directory when started from a terminal inside a repository. Only an
// argument that is not a repository is an error worth showing.
func startPath() (string, bool) {
	for _, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, "-") {
			return arg, true
		}
	}
	wd, err := os.Getwd()
	if err != nil || wd == "/" {
		return "", false
	}
	if _, err := gitx.Open(wd); err != nil {
		return "", false
	}
	return wd, true
}

func main() {
	var a *app
	mygo.App.WhenReady(func() {
		dir, err := mygo.App.Path(mygo.PathUserData)
		if err != nil {
			log.Fatal(err)
		}
		a = newApp(store.Store{Dir: dir})
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "File", Submenu: []*mygo.MenuItem{
				{Label: "Open Repository…", Accelerator: "CmdOrCtrl+O", Click: func(*mygo.MenuItem, *mygo.Window) { a.chooseRepo() }},
				{Label: "Refresh", Accelerator: "CmdOrCtrl+R", Click: func(*mygo.MenuItem, *mygo.Window) {
					if a.repo != nil {
						a.reload()
						a.win.Invalidate()
					}
				}},
				mygo.Separator(),
				{Role: mygo.RoleClose},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "AgentDiff",
			Width:     1400,
			Height:    900,
			MinWidth:  900,
			MinHeight: 560,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		if p, ok := startPath(); ok {
			a.win.Update(func() { a.openRepo(p) })
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
