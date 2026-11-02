package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestKeyLabel(t *testing.T) {
	tests := []struct {
		goos, keys, want string
	}{
		{"darwin", "Shift+CmdOrCtrl+P", "⇧⌘P"},
		{"darwin", "Ctrl+Alt+CmdOrCtrl+Down", "⌃⌥⌘↓"},
		{"darwin", "CmdOrCtrl+Enter", "⌘↩"},
		{"windows", "Shift+CmdOrCtrl+P", "Ctrl+Shift+P"},
		{"windows", "Alt+CmdOrCtrl+Down", "Ctrl+Alt+Down"},
		{"linux", "CmdOrCtrl+,", "Ctrl+,"},
		{"linux", "Control+Enter", "Ctrl+Enter"},
		{"windows", "Cmd+K", "Cmd+K"},
		{"darwin", "Hyper+K", "Hyper+K"},
	}
	for _, tt := range tests {
		if got := platformOf(tt.goos).keyLabel(tt.keys); got != tt.want {
			t.Errorf("%s keyLabel(%q) = %q, want %q", tt.goos, tt.keys, got, tt.want)
		}
	}
}

func TestModsLabel(t *testing.T) {
	if got := platformOf("darwin").modsLabel(ui.Shift | ui.Super); got != "⇧⌘" {
		t.Errorf("macOS = %q", got)
	}
	if got := platformOf("windows").modsLabel(ui.Shift | ui.Ctrl | ui.Super); got != "Ctrl+Shift+" {
		t.Errorf("Windows = %q", got)
	}
}

func TestAccelerator(t *testing.T) {
	tests := []struct {
		goos string
		mods ui.Modifiers
		want string
	}{
		{"darwin", ui.Ctrl, "Ctrl+K"},
		{"darwin", ui.Super | ui.Shift, "Shift+CmdOrCtrl+K"},
		{"windows", ui.Ctrl, "CmdOrCtrl+K"},
		{"linux", ui.Ctrl | ui.Alt, "Alt+CmdOrCtrl+K"},
	}
	for _, tt := range tests {
		if got := platformOf(tt.goos).accelerator(tt.mods, ui.KeyK); got != tt.want {
			t.Errorf("%s accelerator(%v) = %q, want %q", tt.goos, tt.mods, got, tt.want)
		}
	}
}

func TestRecordedControlKeysOffMac(t *testing.T) {
	app := newTestApp(t)
	app.platform = platformOf("windows")
	app.km = nil
	app.startRecording("refresh")
	app.keyInput(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyK, Mods: ui.Ctrl | ui.Shift})
	if got := app.keymap().of("refresh"); got != "Shift+CmdOrCtrl+K" {
		t.Errorf("recorded keys = %q", got)
	}
	app.startRecording("refresh")
	if app.keyInput(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyJ, Mods: ui.Super}) {
		t.Error("keys with the Windows key alone were recorded")
	}
}
