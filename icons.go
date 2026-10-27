package main

import "github.com/egoist/mygo/ui"

// icon parses the shapes of a 24x24 stroked icon, drawn in the text color.
func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	chevronIcon  = icon(`<path d="m6 9 6 6 6-6"/>`)
	folderIcon   = icon(`<path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.7-.9l-.8-1.2A2 2 0 0 0 7.9 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2z"/>`)
	branchIcon   = icon(`<path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>`)
	compareIcon  = icon(`<path d="M4 7h10M18 7h2M4 17h2M10 17h10"/><circle cx="16" cy="7" r="2"/><circle cx="8" cy="17" r="2"/>`)
	plusIcon     = icon(`<path d="M12 5v14M5 12h14"/>`)
	trashIcon    = icon(`<path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>`)
	checkIcon    = icon(`<path d="M20 6 9 17l-5-5"/>`)
	editIcon     = icon(`<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/>`)
	commitIcon   = icon(`<circle cx="12" cy="12" r="3"/><path d="M3 12h6M15 12h6"/>`)
	layersIcon   = icon(`<path d="M12 3 3 8l9 5 9-5z"/><path d="m3 13 9 5 9-5"/>`)
	overviewIcon = icon(`<path d="M2 4h6a4 4 0 0 1 4 4v13a3 3 0 0 0-3-3H2z"/><path d="M22 4h-6a4 4 0 0 0-4 4v13a3 3 0 0 1 3-3h7z"/>`)
	messageIcon  = icon(`<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>`)
	sendIcon     = icon(`<path d="m22 2-7 20-4-9-9-4z"/><path d="M22 2 11 13"/>`)
	worktreeIcon = icon(`<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/>`)
)
