package app

import "maps"

// keymap holds the shortcuts of the commands: the app's defaults with the
// user's changes over them. No two commands share keys, and no command
// has keys that something the user cannot change uses. It keeps keys in
// the form accelerator writes, so keys that name the same keys compare
// equal.
type keymap struct {
	defaults map[string]string
	keys     map[string]string
	// reserved names what uses the keys no command can take.
	reserved map[string]string
}

type assignKind uint8

const (
	// assignUnchanged: the command already has the keys.
	assignUnchanged assignKind = iota
	// assignTaken: the keys are reserved. other names what uses them.
	assignTaken
	// assignMoved: other, a command id, had the keys and lost them.
	assignMoved
	assignSet
)

// assignment is what assign did.
type assignment struct {
	kind  assignKind
	other string
}

// newKeymap gives cmds their defaults, with user over them: the user's
// keys by command id, "" for none. It skips the ids of no command and keys
// it cannot read. Of two commands with the same keys, the first in cmds
// keeps them, and the other gets its default back, or no keys when the
// default is taken.
func newKeymap(cmds []*command, reserved map[string]string, user map[string]string) *keymap {
	keymap := &keymap{defaults: map[string]string{}, keys: map[string]string{}, reserved: map[string]string{}}
	for keys, use := range reserved {
		keymap.reserved[canonical(keys)] = use
	}
	owner := map[string]string{}
	var lost []string
	for _, cm := range cmds {
		keymap.defaults[cm.id] = canonical(cm.keys)
		keys := keymap.defaults[cm.id]
		if override, ok := user[cm.id]; ok && (override == "" || canonical(override) != "") {
			keys = canonical(override)
		}
		if keys != "" && (owner[keys] != "" || keymap.reserved[keys] != "") {
			lost = append(lost, cm.id)
			continue
		}
		keymap.keys[cm.id] = keys
		if keys != "" {
			owner[keys] = cm.id
		}
	}
	for _, id := range lost {
		if def := keymap.defaults[id]; def != "" && owner[def] == "" {
			keymap.keys[id] = def
			owner[def] = id
		} else {
			keymap.keys[id] = ""
		}
	}
	return keymap
}

// of are the keys of the command id, "" for none.
func (keymap *keymap) of(id string) string { return keymap.keys[id] }

// changed tells whether the command id has other keys than its default.
func (keymap *keymap) changed(id string) bool { return keymap.keys[id] != keymap.defaults[id] }

// assign gives the command id the keys, "" for none. Reserved keys stay
// where they are, and keys another command has move from it.
func (keymap *keymap) assign(id, keys string) assignment {
	keys = canonical(keys)
	if keys == keymap.keys[id] {
		return assignment{kind: assignUnchanged}
	}
	if use := keymap.reserved[keys]; keys != "" && use != "" {
		return assignment{kind: assignTaken, other: use}
	}
	res := assignment{kind: assignSet}
	for other, has := range keymap.keys {
		if keys != "" && other != id && has == keys {
			keymap.keys[other] = ""
			res = assignment{kind: assignMoved, other: other}
		}
	}
	keymap.keys[id] = keys
	return res
}

// reset gives the commands ids their default keys back, every command for
// none. A command that has one of them loses it.
func (keymap *keymap) reset(ids ...string) {
	if len(ids) == 0 {
		keymap.keys = maps.Clone(keymap.defaults)
		return
	}
	for _, id := range ids {
		keymap.assign(id, keymap.defaults[id])
	}
}

// overrides are the keys of the commands that differ from their defaults,
// by command id, "" for none: what the config keeps.
func (keymap *keymap) overrides() map[string]string {
	out := map[string]string{}
	for id := range keymap.keys {
		if keymap.changed(id) {
			out[id] = keymap.keys[id]
		}
	}
	return out
}

// canonical writes keys the way accelerator does, or "" for keys it
// cannot read.
func canonical(keys string) string {
	mods, key, ok := parseAccelerator(keys)
	if !ok {
		return ""
	}
	return accelerator(mods, key)
}
