package app

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// startRecording records the next keys pressed as the command's shortcut.
func (app *app) startRecording(id string) {
	app.recording, app.recordStart, app.keysNote = id, true, ""
}

// recordKeys ends the recording with keys, unless something the user
// cannot change takes them.
func (app *app) recordKeys(keys string) {
	cm := app.cmd(app.recording)
	app.recording = ""
	switch as := app.keymap().assign(cm.id, keys); as.kind {
	case assignUnchanged:
		return
	case assignTaken:
		app.keysNote = keyLabel(keys) + " is taken by " + as.other + ", which you cannot change."
		return
	case assignMoved:
		app.keysNote = keyLabel(keys) + " moved from " + app.cmd(as.other).title + " to " + cm.title + "."
	default:
		app.keysNote = ""
	}
	app.keysChanged()
}

// keyInput records a key pressed with Command or Control. Escape cancels,
// and other keys, as Tab, do what they do.
func (app *app) keyInput(ev ui.InputEvent) bool {
	if ev.Kind != ui.InputKeyDown {
		return false
	}
	mods := ev.Mods & (ui.Ctrl | ui.Alt | ui.Shift | ui.Cmd)
	switch {
	case ev.Key == ui.KeyEscape && mods == 0:
		app.recording = ""
		return true
	case mods&(ui.Cmd|ui.Ctrl) == 0:
		return false
	}
	if keys := accelerator(mods, ev.Key); keys != "" {
		app.recordKeys(keys)
	}
	return true
}

// shortcutsTab lists the commands with their shortcuts, which a click
// on the keys records anew.
func (app *app) shortcutsTab(ctx *ui.Context) {
	theme := ctx.Theme()
	filter := strings.ToLower(strings.TrimSpace(app.keysFilter))
	var cmds []*command
	for _, cm := range app.cachedCommands() {
		if strings.Contains(strings.ToLower(cm.title), filter) || filter != "" && strings.Contains(strings.ToLower(keyLabel(app.keymap().of(cm.id))), filter) {
			cmds = append(cmds, cm)
		}
	}
	slices.SortFunc(cmds, func(x, y *command) int { return strings.Compare(x.title, y.title) })
	ui.Text(ctx, "Click a shortcut and press the new keys, with ⌘ or ⌃. Escape cancels. A shortcut another command has moves to this one.").FontSize(12).TextColor(theme.TextMuted)
	ui.TextInput(ctx, &app.keysFilter).Placeholder("Filter commands").Label("Filter commands")
	ui.Scroll(ctx).Shrink(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(ctx).Gap(2).PaddingEnd(4).Children(func() {
			for _, cm := range cmds {
				app.shortcutRow(ctx.Key(cm.id), cm)
			}
			if len(cmds) == 0 {
				ui.Text(ctx, "No commands match").FontSize(12).TextColor(theme.TextMuted).Padding(6, 0)
			}
		})
	})
	ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(ctx, app.keysNote).FontSize(12).TextColor(theme.TextMuted).Grow(1).Shrink(1)
		ui.Button(ctx, "Reset all shortcuts").Disabled(len(app.cfg.Keys) == 0).OnClick(func() {
			app.keymap().reset()
			app.keysChanged()
			app.keysNote = ""
		})
	})
}

func (app *app) shortcutRow(ctx *ui.Context, cm *command) {
	theme := ctx.Theme()
	keys := app.keymap().of(cm.id)
	changed := app.keymap().changed(cm.id)
	ui.Row(ctx).Gap(8).Padding(2, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(ctx, strings.TrimSuffix(cm.title, "…")).Grow(1).Shrink(1).SingleLine()
		if app.recording == cm.id {
			rec := ui.Row(ctx).Padding(4, 10).MinWidth(120).Radius(theme.Radius).Border(1, theme.Accent).Background(theme.Surface).
				Focusable().Label("Press the new shortcut for " + cm.title + ", or Escape to cancel").HandleInput(app.keyInput).
				Children(func() {
					// The modifiers show as they are held, before the key
					// that ends the recording.
					if held := modsLabel(ctx.Modifiers()); held != "" {
						ui.Text(ctx, held+"…").Font("monospace").FontSize(12)
					} else {
						ui.Text(ctx, "Press keys…").FontSize(12).TextColor(theme.TextMuted)
					}
				})
			switch {
			case app.recordStart:
				rec.Focus()
				app.recordStart = false
			case !rec.Focused():
				// The focus moved away: a click elsewhere, or Tab.
				app.recording = ""
			}
		} else {
			label := "None"
			if keys != "" {
				label = keyLabel(keys)
			}
			button := ui.Button(ctx, label).MinWidth(120).Label("Shortcut for " + cm.title + ": " + label + ". Press to record another")
			if keys == "" {
				button.TextColor(theme.TextMuted)
			} else {
				button.Font("monospace")
			}
			if button.Clicked() {
				app.startRecording(cm.id)
			}
		}
		remove := iconButton(ctx, trashIcon, "Remove the shortcut of "+cm.title, "Remove the shortcut")
		if keys == "" {
			remove.Invisible()
		} else if remove.Clicked() {
			app.keymap().assign(cm.id, "")
			app.keysChanged()
			app.keysNote = ""
		}
		reset := ui.Button(ctx, "Reset").Label("Reset the shortcut of " + cm.title).Tooltip("Back to " + defaultKeysLabel(cm))
		if !changed {
			reset.Invisible()
		} else if reset.Clicked() {
			app.keymap().reset(cm.id)
			app.keysChanged()
			app.keysNote = ""
		}
	})
}

func defaultKeysLabel(cm *command) string {
	if cm.keys == "" {
		return "no shortcut"
	}
	return keyLabel(cm.keys)
}
