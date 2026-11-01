package app

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// shortcut is a key, or keys, and what they do.
type shortcut struct{ keys, does string }

// otherKeys are the keys that work outside the menu bar, in a part of
// the window.
var otherKeys = []shortcut{
	{"Escape", "Closes the side panel on top, a dialog or a list"},
	{"Tab", "Moves the focus to the next part of the window"},
	{"↑ ↓, Home, End", "Chooses an item of the sidebar that has the focus"},
	{"← →", "Hides and shows the files of the group chosen in the sidebar"},
	{"↓, Return, Space", "Opens the list of the project or branch button"},
	{"Return", "Opens the project or branch highlighted in the list"},
	{"⌘Return", "Opens the worktree of the branch highlighted in the list, or sends the question or comment you are typing"},
	{"⌘⌫", "Removes the project highlighted in the list, after you confirm"},
	{"Click, ⇧Click", "Selects a range of lines on their numbers in a diff"},
}

// menuShortcuts are the shortcuts of the app's own items of the menu
// bar, by menu. Items with a role, as Copy, work as in every app.
func menuShortcuts(menu *mygo.Menu) (menus []string, keys [][]shortcut) {
	for _, m := range menu.Items() {
		var list []shortcut
		for _, it := range m.Submenu {
			if it.Accelerator != "" && it.Role == "" {
				list = append(list, shortcut{keyLabel(it.Accelerator), strings.TrimSuffix(it.Label, "…")})
			}
		}
		if len(list) == 0 {
			continue
		}
		name := m.Label
		if m.Role == mygo.RoleHelp {
			name = "Help"
		}
		menus, keys = append(menus, name), append(keys, list)
	}
	return menus, keys
}

// moreShortcuts are the shortcuts of the commands the menu bar does not
// hold, which they run where they apply.
func (app *app) moreShortcuts() []shortcut {
	inMenu := app.menuIDs()
	var out []shortcut
	for _, cm := range app.cachedCommands() {
		if keys := app.keymap().of(cm.id); keys != "" && !inMenu[cm.id] {
			out = append(out, shortcut{keyLabel(keys), strings.TrimSuffix(cm.title, "…")})
		}
	}
	return out
}

func (app *app) shortcutsModal(ctx *ui.Context) {
	if !app.shortcutsOpen {
		return
	}
	theme := ctx.Theme()
	section := func(title string, list []shortcut) {
		ui.Column(ctx.Key(title)).Gap(6).Children(func() {
			ui.Text(ctx, title).FontSize(12).Bold().TextColor(theme.TextMuted)
			for _, s := range list {
				ui.Row(ctx.Key(s.keys + s.does)).Gap(16).AlignItems(ui.Start).Children(func() {
					ui.Text(ctx, s.does).Grow(1).Shrink(1)
					ui.Text(ctx, s.keys).Font("monospace").FontSize(12).Shrink(0)
				})
			}
		})
	}
	ui.Modal(ctx, &app.shortcutsOpen, func() {
		ui.Column(ctx).Width(560).Shrink(1).MinHeight(0).Children(func() {
			ui.Text(ctx, "Keyboard shortcuts").FontSize(18).Bold().Padding(0, 0, 14, 0)
			ui.Scroll(ctx).Shrink(1).MinHeight(0).FillWidth().Children(func() {
				ui.Column(ctx).Gap(18).PaddingEnd(4).Children(func() {
					menus, keys := menuShortcuts(app.menu())
					for i, m := range menus {
						section(m+" menu", keys[i])
					}
					if more := app.moreShortcuts(); len(more) > 0 {
						section("Other commands", more)
					}
					section("In the window", otherKeys)
				})
			})
			ui.Row(ctx).Gap(8).AlignItems(ui.Center).Padding(14, 0, 0, 0).Children(func() {
				if ui.Button(ctx, "Change shortcuts…").Clicked() {
					app.shortcutsOpen = false
					app.openSettings()
					app.settings.tab = shortcutsTab
				}
				ui.Spacer(ctx)
				app.takeFocus(ui.PrimaryButton(ctx, "Done").Tooltip("Escape").AutoFocus().OnClick(func() { app.shortcutsOpen = false }), "shortcuts")
			})
		})
	})
}
