package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// startRecording records the next keys pressed as the command's shortcut.
func (a *app) startRecording(id string) {
	a.recording, a.recordStart, a.keysNote = id, true, ""
}

// recordKeys ends the recording with keys, unless something the user
// cannot change takes them.
func (a *app) recordKeys(keys string) {
	cm := a.cmd(a.recording)
	a.recording = ""
	if sameKeys(keys, a.keysOf(cm)) {
		return
	}
	if use := a.keysTaken(keys); use != "" {
		a.keysNote = keyLabel(keys) + " is taken by " + use + ", which you cannot change."
		return
	}
	a.keysNote = ""
	if from := a.setKeys(cm.id, keys); from != nil {
		a.keysNote = keyLabel(keys) + " moved from " + from.title + " to " + cm.title + "."
	}
}

// keyInput records a key pressed with Command or Control. Escape cancels,
// and other keys, as Tab, do what they do.
func (a *app) keyInput(ev ui.InputEvent) bool {
	if ev.Kind != ui.InputKeyDown {
		return false
	}
	mods := ev.Mods & (ui.Ctrl | ui.Alt | ui.Shift | ui.Cmd)
	switch {
	case ev.Key == ui.KeyEscape && mods == 0:
		a.recording = ""
		return true
	case mods&(ui.Cmd|ui.Ctrl) == 0:
		return false
	}
	if keys := accelerator(mods, ev.Key); keys != "" {
		a.recordKeys(keys)
	}
	return true
}

// shortcutsTab lists the commands with their shortcuts, which a click
// on the keys records anew.
func (a *app) shortcutsTab(c *ui.Context) {
	t := c.Theme()
	q := strings.ToLower(strings.TrimSpace(a.keysFilter))
	var cmds []*command
	for _, cm := range a.commands() {
		if strings.Contains(strings.ToLower(cm.title), q) || q != "" && strings.Contains(strings.ToLower(keyLabel(a.keysOf(cm))), q) {
			cmds = append(cmds, cm)
		}
	}
	slices.SortFunc(cmds, func(x, y *command) int { return strings.Compare(x.title, y.title) })
	ui.Text(c, "Click a shortcut and press the new keys, with ⌘ or ⌃. Escape cancels. A shortcut another command has moves to this one.").FontSize(12).TextColor(t.TextMuted)
	ui.TextInput(c, &a.keysFilter).Placeholder("Filter commands").Label("Filter commands")
	ui.Scroll(c).MaxHeight(380).FillWidth().Children(func() {
		ui.Column(c).Gap(2).PaddingEnd(4).Children(func() {
			for _, cm := range cmds {
				a.shortcutRow(c.Key(cm.id), cm)
			}
			if len(cmds) == 0 {
				ui.Text(c, "No commands match").FontSize(12).TextColor(t.TextMuted).Padding(6, 0)
			}
		})
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.keysNote).FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1)
		ui.Button(c, "Reset all shortcuts").Disabled(len(a.cfg.Keys) == 0).OnClick(func() {
			a.resetKeys()
			a.keysNote = ""
		})
	})
}

func (a *app) shortcutRow(c *ui.Context, cm *command) {
	t := c.Theme()
	keys := a.keysOf(cm)
	_, changed := a.cfg.Keys[cm.id]
	ui.Row(c).Gap(8).Padding(2, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, strings.TrimSuffix(cm.title, "…")).Grow(1).Shrink(1).SingleLine()
		if a.recording == cm.id {
			rec := ui.Row(c).Padding(4, 10).MinWidth(120).Radius(t.Radius).Border(1, t.Accent).Background(t.Surface).
				Focusable().Label("Press the new shortcut for " + cm.title + ", or Escape to cancel").HandleInput(a.keyInput).
				Children(func() { ui.Text(c, "Press keys…").FontSize(12).TextColor(t.TextMuted) })
			switch {
			case a.recordStart:
				rec.Focus()
				a.recordStart = false
			case !rec.Focused():
				// The focus moved away: a click elsewhere, or Tab.
				a.recording = ""
			}
		} else {
			label := "None"
			if keys != "" {
				label = keyLabel(keys)
			}
			b := ui.Button(c, label).MinWidth(120).Label("Shortcut for " + cm.title + ": " + label + ". Press to record another")
			if keys == "" {
				b.TextColor(t.TextMuted)
			} else {
				b.Font("monospace")
			}
			if b.Clicked() {
				a.startRecording(cm.id)
			}
		}
		remove := iconButton(c, trashIcon, "Remove the shortcut of "+cm.title).Tooltip("Remove the shortcut")
		if keys == "" {
			remove.Invisible()
		} else if remove.Clicked() {
			a.setKeys(cm.id, "")
			a.keysNote = ""
		}
		reset := ui.Button(c, "Reset").Label("Reset the shortcut of " + cm.title).Tooltip("Back to " + defaultKeysLabel(cm))
		if !changed {
			reset.Invisible()
		} else if reset.Clicked() {
			a.resetKeys(cm.id)
			a.keysNote = ""
		}
	})
}

func defaultKeysLabel(cm *command) string {
	if cm.keys == "" {
		return "no shortcut"
	}
	return keyLabel(cm.keys)
}
