package app

// The corner radii, in DIPs. Controls take the theme's Radius.
const (
	// radiusSmall rounds a small button inside another control, and a link.
	radiusSmall = 4
	// radiusBox rounds a box or a row inside a page: a message, a warning,
	// a link row.
	radiusBox = 8
	// radiusCard rounds a card, and a panel over the window.
	radiusCard = 10
)

// The font sizes, in DIPs. Body text takes the theme's FontSize.
const (
	// fontTiny is the text of a dense row: a count, a time, a key hint.
	fontTiny = 11
	// fontSmall is secondary text, code and the text of a small control.
	fontSmall = 12
	// fontLead is the first lines of a page, under its title.
	fontLead = 15
	// fontCard is the title of a card.
	fontCard = 16
	// fontHeading is the title of a dialog or of a part of a page.
	fontHeading = 18
	// fontTitle is the title of a page.
	fontTitle = 22
	// fontHero is the name of the app on the welcome screen.
	fontHero = 30
)

// monoFont is the font of code, paths and keys.
const monoFont = "monospace"
