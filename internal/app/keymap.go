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
	k := &keymap{defaults: map[string]string{}, keys: map[string]string{}, reserved: map[string]string{}}
	for keys, use := range reserved {
		k.reserved[canonical(keys)] = use
	}
	owner := map[string]string{}
	var lost []string
	for _, cm := range cmds {
		k.defaults[cm.id] = canonical(cm.keys)
		keys := k.defaults[cm.id]
		if u, ok := user[cm.id]; ok && (u == "" || canonical(u) != "") {
			keys = canonical(u)
		}
		if keys != "" && (owner[keys] != "" || k.reserved[keys] != "") {
			lost = append(lost, cm.id)
			continue
		}
		k.keys[cm.id] = keys
		if keys != "" {
			owner[keys] = cm.id
		}
	}
	for _, id := range lost {
		if def := k.defaults[id]; def != "" && owner[def] == "" {
			k.keys[id] = def
			owner[def] = id
		} else {
			k.keys[id] = ""
		}
	}
	return k
}

// of are the keys of the command id, "" for none.
func (k *keymap) of(id string) string { return k.keys[id] }

// changed tells whether the command id has other keys than its default.
func (k *keymap) changed(id string) bool { return k.keys[id] != k.defaults[id] }

// assign gives the command id the keys, "" for none. Reserved keys stay
// where they are, and keys another command has move from it.
func (k *keymap) assign(id, keys string) assignment {
	keys = canonical(keys)
	if keys == k.keys[id] {
		return assignment{kind: assignUnchanged}
	}
	if use := k.reserved[keys]; keys != "" && use != "" {
		return assignment{kind: assignTaken, other: use}
	}
	res := assignment{kind: assignSet}
	for other, has := range k.keys {
		if keys != "" && other != id && has == keys {
			k.keys[other] = ""
			res = assignment{kind: assignMoved, other: other}
		}
	}
	k.keys[id] = keys
	return res
}

// reset gives the commands ids their default keys back, every command for
// none. A command that has one of them loses it.
func (k *keymap) reset(ids ...string) {
	if len(ids) == 0 {
		k.keys = maps.Clone(k.defaults)
		return
	}
	for _, id := range ids {
		k.assign(id, k.defaults[id])
	}
}

// overrides are the keys of the commands that differ from their defaults,
// by command id, "" for none: what the config keeps.
func (k *keymap) overrides() map[string]string {
	out := map[string]string{}
	for id := range k.keys {
		if k.changed(id) {
			out[id] = k.keys[id]
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
