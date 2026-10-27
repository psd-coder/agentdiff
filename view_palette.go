package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// maxRecentCommands is how many commands the palette keeps as used last.
const maxRecentCommands = 5

func (a *app) openPalette() {
	a.paletteOpen, a.paletteQuery = true, ""
	a.paletteNav.open(0)
}

// usedCommand puts the command first among the ones run last from the
// palette.
func (a *app) usedCommand(id string) {
	recent := slices.DeleteFunc(slices.Clone(a.cfg.RecentCommands), func(r string) bool { return r == id })
	recent = append([]string{id}, recent...)
	a.cfg.RecentCommands = recent[:min(len(recent), maxRecentCommands)]
	a.saveConfig()
}

// paletteCommands are the commands that apply here and whose title holds
// the query, in the palette's order: while the query is empty, the ones
// used last come first.
func (a *app) paletteCommands() []*command {
	q := strings.ToLower(strings.TrimSpace(a.paletteQuery))
	var out []*command
	for _, cm := range a.commands() {
		if _, ok := a.here(cm); ok && cm.id != "palette" && strings.Contains(strings.ToLower(cm.title), q) {
			out = append(out, cm)
		}
	}
	if q == "" {
		rank := func(cm *command) int {
			if i := slices.Index(a.cfg.RecentCommands, cm.id); i >= 0 {
				return i
			}
			return maxRecentCommands
		}
		slices.SortStableFunc(out, func(x, y *command) int { return rank(x) - rank(y) })
	}
	return out
}

func (a *app) paletteModal(c *ui.Context) {
	if !a.paletteOpen {
		return
	}
	t := c.Theme()
	cmds := a.paletteCommands()
	nav := &a.paletteNav
	run := func(cm *command) {
		a.paletteOpen = false
		a.runHere(c, cm.id)
		a.usedCommand(cm.id)
	}
	ui.DialogBase(c, &a.paletteOpen, func(back, panel ui.Element) {
		// At the top of the window, so the field stays in place as the
		// filter shortens the list.
		back.Background(ui.RGBA(0, 0, 0, 0.2)).Justify(ui.Start).Padding(64, 16, 16, 16)
		panel.Width(600).Shrink(1).Padding(8).Gap(6).Radius(10).Background(t.Background).Border(1, t.Border).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Label("Command palette")
		in := searchField(c, &a.paletteQuery, "Type a command", func(k ui.TextKey) bool {
			switch {
			case k.Mods == 0 && nav.key(k.Key, len(cmds)):
				return true
			case k.Mods == 0 && k.Key == ui.KeyEnter && nav.hi < len(cmds):
				run(cmds[nav.hi])
				return true
			}
			return false
		})
		if in.Changed() {
			nav.open(0)
		}
		ui.Scroll(c).MaxHeight(360).Children(func() {
			nav.list(c, "Commands", func() {
				for _, cm := range cmds {
					keys := a.keysOf(cm)
					row := nav.row(c.Key(cm.id), func() {
						ui.Text(c, cm.title).SingleLine().Shrink(1).Grow(1)
						if keys != "" {
							ui.Text(c, keyLabel(keys)).Font("monospace").FontSize(12).TextColor(t.TextMuted)
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
				ui.Text(c, "No commands match").FontSize(12).TextColor(t.TextMuted).Padding(6, 8)
			}
		})
		if row, ok := nav.active(); ok && len(cmds) > 0 {
			in.ActiveDescendant(row)
		}
	})
}
