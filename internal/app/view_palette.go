package app

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// maxRecentCommands is how many commands the palette keeps as used last.
const maxRecentCommands = 5

func (app *app) openPalette() {
	app.paletteOpen, app.paletteQuery = true, ""
	app.paletteNav.open(0)
}

// usedCommand puts the command first among the ones run last from the
// palette.
func (app *app) usedCommand(id string) {
	recent := slices.DeleteFunc(slices.Clone(app.cfg.RecentCommands), func(r string) bool { return r == id })
	recent = append([]string{id}, recent...)
	app.cfg.RecentCommands = recent[:min(len(recent), maxRecentCommands)]
	app.saveConfig()
}

// paletteCommands are the commands that apply here and whose title holds
// the query, in the palette's order: while the query is empty, the ones
// used last come first.
func (app *app) paletteCommands() []*command {
	query := strings.ToLower(strings.TrimSpace(app.paletteQuery))
	var out []*command
	for _, cm := range app.cachedCommands() {
		if _, ok := app.here(cm); ok && cm.id != "palette" && strings.Contains(strings.ToLower(cm.title), query) {
			out = append(out, cm)
		}
	}
	if query == "" {
		rank := func(cm *command) int {
			if index := slices.Index(app.cfg.RecentCommands, cm.id); index >= 0 {
				return index
			}
			return maxRecentCommands
		}
		slices.SortStableFunc(out, func(x, y *command) int { return rank(x) - rank(y) })
	}
	return out
}

func (app *app) paletteModal(ctx *ui.Context) {
	if !app.paletteOpen {
		return
	}
	theme := ctx.Theme()
	cmds := app.paletteCommands()
	nav := &app.paletteNav
	run := func(cm *command) {
		app.paletteOpen = false
		app.runHere(ctx, cm.id)
		app.usedCommand(cm.id)
	}
	ui.DialogBase(ctx, &app.paletteOpen, func(back, panel ui.Element) {
		// At the top of the window, so the field stays in place as the
		// filter shortens the list.
		back.Background(ui.RGBA(0, 0, 0, 0.2)).Justify(ui.Start).Padding(64, 16, 16, 16)
		panel.Width(600).Shrink(1).Padding(8).Gap(6).Radius(10).Background(theme.Background).Border(1, theme.Border).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Label("Command palette")
		in := searchField(ctx, &app.paletteQuery, "Type a command", func(k ui.TextKey) bool {
			switch {
			case k.Mods == 0 && nav.key(k.Key, len(cmds)):
				return true
			case k.Mods == 0 && k.Key == ui.KeyEnter && nav.hi < len(cmds):
				run(cmds[nav.hi])
				return true
			}
			return false
		})
		app.takeFocus(in, "palette")
		if in.Changed() {
			nav.open(0)
		}
		var rows []ui.Element
		ui.Scroll(ctx).MaxHeight(360).Children(func() {
			_, rows = nav.list(ctx, "Commands", func(addRow pickRow) {
				for _, cm := range cmds {
					keys := app.keymap().of(cm.id)
					row := addRow(ctx.Key(cm.id), func() {
						ui.Text(ctx, cm.title).SingleLine().Shrink(1).Grow(1)
						if keys != "" {
							ui.Text(ctx, keyLabel(keys)).Font("monospace").FontSize(12).TextColor(theme.TextMuted)
						}
					}, nil)
					if keys != "" {
						row.Label(cm.title + ", " + keyLabel(keys))
					}
					if row.Clicked() {
						run(cm)
					}
				}
			})
			if len(cmds) == 0 {
				ui.Text(ctx, "No commands match").FontSize(12).TextColor(theme.TextMuted).Padding(6, 8)
			}
		})
		if row, ok := nav.active(rows); ok && len(cmds) > 0 {
			in.ActiveDescendant(row)
		}
	})
}
