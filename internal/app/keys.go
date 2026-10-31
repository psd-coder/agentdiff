package app

import (
	"maps"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// keyNames are the names of keys in accelerators, other than letters and
// digits.
var keyNames = map[ui.Key]string{
	ui.KeyEnter: "Enter", ui.KeyEscape: "Escape", ui.KeyBackspace: "Backspace", ui.KeyTab: "Tab",
	ui.KeySpace: "Space", ui.KeyDelete: "Delete", ui.KeyHome: "Home", ui.KeyEnd: "End",
	ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
	ui.KeyLeft: "Left", ui.KeyRight: "Right", ui.KeyUp: "Up", ui.KeyDown: "Down",
	ui.KeyF1: "F1", ui.KeyF2: "F2", ui.KeyF3: "F3", ui.KeyF4: "F4", ui.KeyF5: "F5", ui.KeyF6: "F6",
	ui.KeyF7: "F7", ui.KeyF8: "F8", ui.KeyF9: "F9", ui.KeyF10: "F10", ui.KeyF11: "F11", ui.KeyF12: "F12",
	ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".", ui.KeySlash: "/",
	ui.KeySemicolon: ";", ui.KeyQuote: "'", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]",
	ui.KeyBackslash: "\\", ui.KeyBackquote: "`",
}

// keySigns are how the menus of macOS show some keys.
var keySigns = map[string]string{
	"Up": "↑", "Down": "↓", "Left": "←", "Right": "→", "Enter": "↩", "Backspace": "⌫", "Delete": "⌦",
	"Escape": "⎋", "Tab": "⇥", "PageUp": "⇞", "PageDown": "⇟", "Home": "↖", "End": "↘",
}

// modNames are the modifiers of accelerators, in the order macOS shows
// them: Control, Option, Shift, Command.
var modNames = []struct {
	mod   ui.Modifiers
	name  string
	sign  string
	alias []string
}{
	{ui.Ctrl, "Ctrl", "⌃", []string{"Control"}},
	{ui.Alt, "Alt", "⌥", []string{"Option"}},
	{ui.Shift, "Shift", "⇧", nil},
	{ui.Cmd, "CmdOrCtrl", "⌘", []string{"Cmd", "Command", "CommandOrControl"}},
}

// accelerator writes a key with modifiers as the menu bar takes it, as
// "CmdOrCtrl+Shift+P". It is empty for a key it has no name for.
func accelerator(mods ui.Modifiers, key ui.Key) string {
	name := keyNames[key]
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		name = string(rune('A' + key - ui.KeyA))
	case key >= ui.Key0 && key <= ui.Key9:
		name = string(rune('0' + key - ui.Key0))
	}
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, m := range modNames {
		if mods&m.mod != 0 {
			b.WriteString(m.name)
			b.WriteString("+")
		}
	}
	return b.String() + name
}

// parseAccelerator reads an accelerator that accelerator writes, or one
// with its modifiers in another order.
func parseAccelerator(acc string) (ui.Modifiers, ui.Key, bool) {
	parts := strings.Split(acc, "+")
	var mods ui.Modifiers
	for _, p := range parts[:len(parts)-1] {
		found := false
		for _, m := range modNames {
			if strings.EqualFold(p, m.name) || slices.ContainsFunc(m.alias, func(a string) bool { return strings.EqualFold(a, p) }) {
				mods |= m.mod
				found = true
			}
		}
		if !found {
			return 0, 0, false
		}
	}
	name := parts[len(parts)-1]
	if len(name) == 1 {
		switch r := strings.ToUpper(name)[0]; {
		case r >= 'A' && r <= 'Z':
			return mods, ui.KeyA + ui.Key(r-'A'), true
		case r >= '0' && r <= '9':
			return mods, ui.Key0 + ui.Key(r-'0'), true
		}
	}
	for k, n := range keyNames {
		if strings.EqualFold(n, name) || name == "Return" && k == ui.KeyEnter {
			return mods, k, true
		}
	}
	return 0, 0, false
}

// keyLabel writes an accelerator the way the menus of macOS show it: ⇧⌘B.
func keyLabel(acc string) string {
	mods, key, ok := parseAccelerator(acc)
	if !ok {
		return acc
	}
	name := accelerator(0, key)
	if s, ok := keySigns[name]; ok {
		name = s
	}
	return modsLabel(mods) + name
}

// modsLabel writes modifiers the way the menus of macOS show them: ⌃⌥⇧⌘.
func modsLabel(mods ui.Modifiers) string {
	var b strings.Builder
	for _, m := range modNames {
		if mods&m.mod != 0 {
			b.WriteString(m.sign)
		}
	}
	return b.String()
}

// fixedKeys are keys the app uses in fields and lists, which no command
// can take.
var fixedKeys = map[string]string{
	"CmdOrCtrl+Enter":     "sending a comment or a question",
	"CmdOrCtrl+Backspace": "removing a project from the list",
}

// keymap is the shortcuts of the commands, built from the config once.
// The keys of the standard items of the menu bar, as Copy, and the fixed
// keys are reserved. Of two commands with the same keys in the config,
// the one higher in the menu bar keeps them. The config keeps the
// keymap's overrides from then on.
func (a *app) keymap() *keymap {
	if a.km != nil {
		return a.km
	}
	reserved := maps.Clone(fixedKeys)
	var order []*command
	var walk func(items []*mygo.MenuItem)
	walk = func(items []*mygo.MenuItem) {
		for _, it := range items {
			if it.Role != "" && it.Accelerator != "" {
				reserved[it.Accelerator] = it.Label
			}
			if it.ID != "" {
				order = append(order, a.cmd(it.ID))
			}
			walk(it.Submenu)
		}
	}
	walk(a.menuBar(func(string) string { return "" }).Items())
	for _, cm := range a.cachedCommands() {
		if !slices.Contains(order, cm) {
			order = append(order, cm)
		}
	}
	a.km = newKeymap(order, reserved, a.cfg.Keys)
	a.cfg.Keys = a.km.overrides()
	return a.km
}

// keysChanged saves the keys, and has the next frame put them in the
// menu bar: a change can come from a click on the menu bar itself.
func (a *app) keysChanged() {
	a.cfg.Keys = a.keymap().overrides()
	a.saveConfig()
	a.menuDirty = true
	if a.win != nil {
		a.win.Invalidate()
	}
}

// updateMenu puts the keys changed since the last frame in the menu bar.
func (a *app) updateMenu() {
	if a.menuDirty && a.win != nil {
		mygo.App.SetMenu(a.menu())
	}
	a.menuDirty = false
}

// commandKeys runs, by their keys, the commands the menu bar does not
// hold, while no shortcut is being recorded.
func (a *app) commandKeys(c *ui.Context) {
	if a.recording != "" {
		return
	}
	inMenu := a.menuIDs()
	for _, cm := range a.cachedCommands() {
		keys := a.keymap().of(cm.id)
		if keys == "" || inMenu[cm.id] {
			continue
		}
		if mods, key, ok := parseAccelerator(keys); ok && c.Shortcut(mods, key) {
			a.runHere(c, cm.id)
		}
	}
}
