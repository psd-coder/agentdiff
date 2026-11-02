package app

import "github.com/egoist/mygo/ui"

// platform holds the conventions of the system the app runs on: how
// shortcuts look, where the menu bar puts Settings, and the name of the
// file manager.
type platform struct {
	// mac shows shortcuts as symbols (⇧⌘P), and puts Settings in the app
	// menu and Keyboard Shortcuts in the Help menu of the system.
	mac         bool
	fileManager string
	// enter is the name of the Enter key, which macOS calls Return.
	enter string
}

func platformOf(goos string) platform {
	switch goos {
	case "darwin":
		return platform{mac: true, fileManager: "Finder", enter: "Return"}
	case "windows":
		return platform{fileManager: "File Explorer", enter: "Enter"}
	}
	return platform{fileManager: "File Manager", enter: "Enter"}
}

// cmdEnter names Command or Control with Enter in words, as "⌘Return",
// for a hint under a text field.
func (sys platform) cmdEnter() string { return sys.modsLabel(sys.cmd()) + sys.enter }

// cmd is the modifier that CmdOrCtrl names: Command on macOS, Control
// elsewhere.
func (sys platform) cmd() ui.Modifiers {
	if sys.mac {
		return ui.Super
	}
	return ui.Ctrl
}
