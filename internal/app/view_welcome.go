package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The size of the welcome window.
const welcomeWidth, welcomeHeight = 760, 460

// appIcon is the icon of the app, which the welcome window shows. Nil
// shows none.
var appIcon *ui.Bitmap

// showWelcome shows the welcome window in place of the main window, which
// hides until a repository opens. The hidden main window still runs the
// updates of the app. The windows change after the frame that asked, as
// a window must not close while it builds.
func (app *app) showWelcome() {
	if app.win == nil {
		return
	}
	app.win.Update(func() {
		if app.repo != nil {
			return
		}
		app.win.Hide()
		if app.welcomeWin != nil {
			app.welcomeWin.Focus()
			return
		}
		welcome := mygo.NewWindow(mygo.WindowOptions{
			Title:             "Welcome to AgentDiff",
			Width:             welcomeWidth,
			Height:            welcomeHeight,
			TitleBarStyle:     mygo.TitleBarHidden,
			DisableResize:     true,
			DisableMinimize:   true,
			DisableMaximize:   true,
			DisableFullScreen: true,
			Vibrancy:          mygo.VibrancySidebar,
			Content:           ui.View(app.welcomeView),
		})
		app.welcomeWin = welcome
		welcome.OnClosed(func() {
			app.update(func() {
				if app.welcomeWin == welcome {
					app.welcomeWin = nil
				}
				// Without a repository, only the hidden main window is
				// left, and the app has nothing to show.
				if app.repo == nil {
					mygo.App.Quit()
				}
			})
		})
	})
}

// hideWelcome closes the welcome window and shows the main window.
func (app *app) hideWelcome() {
	if app.win == nil {
		return
	}
	app.win.Update(func() {
		if app.repo == nil {
			return
		}
		app.win.Show()
		if welcome := app.welcomeWin; welcome != nil {
			app.welcomeWin = nil
			welcome.Close()
		}
	})
}

// frontWindow is the window that dialogs belong to: the welcome window
// while it shows, else the main window.
func (app *app) frontWindow() *mygo.Window {
	if app.welcomeWin != nil {
		return app.welcomeWin
	}
	return app.win
}

func (app *app) chooseRepo() {
	parent := app.frontWindow()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: "Open a git repository", Directory: true})
		if err != nil || len(paths) == 0 {
			return
		}
		app.update(func() { app.openRepo(paths[0]) })
	}()
}

// welcomeView is the welcome window, as the one of Xcode: the app and the
// button that opens a repository on the left, the recent repositories on
// the right, over the window's material.
func (app *app) welcomeView(ctx *ui.Context) {
	app.runQueued(ctx)
	app.updateMenu()
	theme := ctx.Theme()
	if ctx.Vibrancy() {
		ctx.Root().Background(ui.Transparent)
	}
	ui.Row(ctx).Fill().AlignItems(ui.Stretch).Children(func() {
		ui.Column(ctx).Grow(1).Center().Gap(8).Padding(32).Background(theme.Background).DragWindow().Children(func() {
			if appIcon != nil {
				ui.Image(ctx, appIcon).Size(128, 128).Fit(ui.Contain)
			}
			ui.Text(ctx, "AgentDiff").FontSize(30).Bold()
			if version := mygo.App.Version(); version != "" && app.win != nil {
				ui.Text(ctx, "Version "+version).TextColor(theme.TextMuted)
			}
			ui.Text(ctx, "Review what your coding agent changed: an explanation first, then the diff, grouped, with comments you send back.").
				TextColor(theme.TextMuted).MaxWidth(340).TextAlign(ui.Center).Margin(8, 0, 0, 0)
			ui.PrimaryButton(ctx, "Open repository…").Margin(16, 0, 0, 0).Tooltip(app.keyTip("Open a repository", "open-repo")).OnClick(app.chooseRepo)
			if app.repoErr != "" {
				ui.Text(ctx, app.repoErr).TextColor(theme.Danger).MaxWidth(340).Selectable()
			}
			if app.saveErr != "" {
				ui.Text(ctx, "Could not save the settings: "+app.saveErr).TextColor(theme.Danger).MaxWidth(340).Selectable()
			}
		})
		ui.Column(ctx).Width(320).Shrink(0).Padding(titleBarHeight, 10, 10, 10).Gap(4).
			Background(sidebarBackground(ctx)).BorderWidth(0, 0, 0, 1).BorderColor(theme.Border).DragWindow().Children(func() {
			if len(app.cfg.Recent) == 0 {
				ui.Text(ctx, "No recent repositories").TextColor(theme.TextMuted).AlignSelf(ui.Center).Margin(24, 0, 0, 0)
				return
			}
			ui.Text(ctx, "Recent").FontSize(11).Bold().TextColor(theme.TextMuted).Padding(0, 8, 4, 8)
			ui.Scroll(ctx).Grow(1).MinHeight(0).FillWidth().Children(func() {
				app.projectList(ctx, &app.projectNav, "", false, func(i int) { app.openRepo(app.cfg.Recent[i]) })
			})
		})
	})
}
