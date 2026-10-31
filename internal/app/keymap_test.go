package app

import (
	"maps"
	"testing"
)

func testKeymap(user map[string]string) *keymap {
	cmds := []*command{
		{id: "refresh", title: "Refresh", keys: "CmdOrCtrl+R"},
		{id: "palette", title: "Command Palette", keys: "CmdOrCtrl+Shift+P"},
		{id: "mark-viewed", title: "Mark File as Viewed"},
		{id: "next-file", title: "Next File", keys: "Alt+CmdOrCtrl+Down"},
	}
	reserved := map[string]string{"CmdOrCtrl+C": "Copy", "CmdOrCtrl+Enter": "sending a comment"}
	return newKeymap(cmds, reserved, user)
}

func TestKeymapAssign(t *testing.T) {
	tests := []struct {
		name      string
		id, keys  string
		want      assignment
		overrides map[string]string
	}{
		{"free keys", "mark-viewed", "CmdOrCtrl+Shift+Y", assignment{kind: assignSet},
			map[string]string{"mark-viewed": "Shift+CmdOrCtrl+Y"}},
		{"keys of another command move", "mark-viewed", "Cmd+R", assignment{kind: assignMoved, other: "refresh"},
			map[string]string{"mark-viewed": "CmdOrCtrl+R", "refresh": ""}},
		{"a standard item keeps its keys", "refresh", "CmdOrCtrl+C", assignment{kind: assignTaken, other: "Copy"},
			map[string]string{}},
		{"a fixed key stays", "refresh", "Command+Return", assignment{kind: assignTaken, other: "sending a comment"},
			map[string]string{}},
		{"the same keys do nothing", "palette", "Shift+Cmd+P", assignment{kind: assignUnchanged},
			map[string]string{}},
		{"no keys remove the shortcut", "refresh", "", assignment{kind: assignSet},
			map[string]string{"refresh": ""}},
		{"the default drops the override", "next-file", "Alt+CmdOrCtrl+Down", assignment{kind: assignUnchanged},
			map[string]string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k := testKeymap(nil)
			if got := k.assign(tc.id, tc.keys); got != tc.want {
				t.Errorf("assign = %+v, want %+v", got, tc.want)
			}
			if got := k.overrides(); !maps.Equal(got, tc.overrides) {
				t.Errorf("overrides = %v, want %v", got, tc.overrides)
			}
		})
	}
}

func TestKeymapReset(t *testing.T) {
	k := testKeymap(map[string]string{"refresh": "CmdOrCtrl+Shift+Y", "mark-viewed": "CmdOrCtrl+R", "palette": ""})
	k.reset("refresh")
	if got, want := k.overrides(), map[string]string{"palette": ""}; !maps.Equal(got, want) {
		t.Errorf("reset of one took its default back: %v, want %v", got, want)
	}
	if k.of("refresh") != "CmdOrCtrl+R" || k.of("mark-viewed") != "" || k.changed("refresh") || !k.changed("palette") {
		t.Errorf("refresh %q, mark-viewed %q", k.of("refresh"), k.of("mark-viewed"))
	}
	k.reset()
	if got := k.overrides(); len(got) != 0 || k.of("palette") != "Shift+CmdOrCtrl+P" {
		t.Errorf("reset of all left %v, palette %q", got, k.of("palette"))
	}
}

func TestKeymapLoad(t *testing.T) {
	tests := []struct {
		name string
		user map[string]string
		want map[string]string
	}{
		{"aliases read as the default", map[string]string{"palette": "Command+Shift+P", "refresh": "Cmd+R"},
			map[string]string{}},
		{"an alias override is kept canonical", map[string]string{"mark-viewed": "Control+Option+M"},
			map[string]string{"mark-viewed": "Ctrl+Alt+M"}},
		{"unknown ids and unreadable keys are dropped", map[string]string{"gone": "CmdOrCtrl+G", "refresh": "Hyper+R"},
			map[string]string{}},
		{"the first command keeps shared keys", map[string]string{"refresh": "CmdOrCtrl+M", "next-file": "CmdOrCtrl+M"},
			map[string]string{"refresh": "CmdOrCtrl+M"}},
		{"a loser without its default gets none", map[string]string{"refresh": "Alt+CmdOrCtrl+Down", "next-file": "Alt+Cmd+Down"},
			map[string]string{"refresh": "Alt+CmdOrCtrl+Down", "next-file": ""}},
		{"reserved keys are dropped", map[string]string{"refresh": "CmdOrCtrl+C"},
			map[string]string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := testKeymap(tc.user).overrides(); !maps.Equal(got, tc.want) {
				t.Errorf("overrides = %v, want %v", got, tc.want)
			}
		})
	}
}
