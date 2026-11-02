package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// platform holds the conventions of the system the app runs on: how
// shortcuts look, where the menu bar puts Settings, who draws the title
// bar, and the name of the file manager.
type platform struct {
	// mac shows shortcuts as symbols (⇧⌘P), puts Settings in the app menu
	// and Keyboard Shortcuts in the Help menu of the system, and has the
	// views draw the title bar under the traffic lights.
	mac bool
	// hideMenuBar keeps the menu bar of the main window out of sight
	// until Alt or F10, as in GNOME's apps, which have none.
	hideMenuBar bool
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
	return platform{hideMenuBar: true, fileManager: "File Manager", enter: "Enter"}
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

// titleBar is style on macOS, where the views draw the title bar. Windows
// and Linux keep the title bar of the system, which also gives the window
// the corners of the system.
func (sys platform) titleBar(style mygo.TitleBarStyle) mygo.TitleBarStyle {
	if sys.mac {
		return style
	}
	return mygo.TitleBarDefault
}
