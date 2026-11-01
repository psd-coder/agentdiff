package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

type codeToken struct {
	color        string
	italic, bold bool
}

// codeVariant holds the colors of a code theme for one appearance. Only a
// theme with one variant sets the colors beside the tokens.
type codeVariant struct {
	dark                                                      bool
	background, text, lineNumber, activeLineNumber, selection string
	tokens                                                    map[editor.TokenKind]codeToken
}

// codeTheme colors code in diffs and in Markdown. A theme with both
// variants follows the window's appearance and colors only the tokens. A
// theme with one variant also paints its own background, because its
// colors only read well on it.
type codeTheme struct {
	name        string
	light, dark *codeVariant
}

// codeThemeNamed returns the theme with that name, or the default one,
// GitHub, for an empty or unknown name.
func codeThemeNamed(name string) codeTheme {
	for _, ct := range codeThemes {
		if ct.name == name {
			return ct
		}
	}
	return codeThemes[0]
}

func codeThemeNames() []string {
	names := make([]string, len(codeThemes))
	for i, ct := range codeThemes {
		names[i] = ct.name
	}
	return names
}

// ownBackground reports whether the theme paints its own background.
func (ct codeTheme) ownBackground() bool {
	return (ct.light == nil) != (ct.dark == nil)
}

// colors returns the editor theme of ct for the window theme t.
func (ct codeTheme) colors(windowTheme *ui.Theme) *editor.Theme {
	variant := ct.light
	if windowTheme.Dark && ct.dark != nil || variant == nil {
		variant = ct.dark
	}
	if variant == nil {
		return editor.DefaultTheme(windowTheme)
	}
	base := windowTheme
	if ct.ownBackground() {
		base = ui.LightTheme()
		if variant.dark {
			base = ui.DarkTheme()
		}
	}
	out := editor.DefaultTheme(base)
	if ct.ownBackground() {
		set := func(c *ui.Color, hex string) {
			if hex != "" {
				*c = ui.Hex(hex)
			}
		}
		set(&out.Background, variant.background)
		set(&out.Text, variant.text)
		set(&out.LineNumber, variant.lineNumber)
		set(&out.ActiveLineNumber, variant.activeLineNumber)
		set(&out.Selection, variant.selection)
		out.InactiveSelection = out.Selection.Alpha(0.55)
	}
	// A token the theme leaves out shows as text, as in VS Code.
	for k := range out.Tokens {
		out.Tokens[k].Color = out.Text
	}
	for k, tok := range variant.tokens {
		style := &out.Tokens[k]
		style.Color = ui.Hex(tok.color)
		style.Italic = style.Italic || tok.italic
		style.Bold = style.Bold || tok.bold
	}
	return out
}

// codeThemeKey is what the code colors are made from.
type codeThemeKey struct {
	name string
	ui   ui.Theme
}

// syncCodeTheme makes the code colors follow the setting and the window
// theme. It keeps them while both stay the same, because Markdown views
// highlight their code again whenever the colors change.
func (app *app) syncCodeTheme(ctx *ui.Context) {
	key := codeThemeKey{name: app.cfg.CodeTheme, ui: *ctx.Theme()}
	if app.codeColors != nil && key == app.codeKey {
		return
	}
	ct := codeThemeNamed(app.cfg.CodeTheme)
	app.codeKey, app.codeColors = key, ct.colors(ctx.Theme())
	app.md.Theme = &markdown.Theme{CodeColors: app.codeColors}
	if ct.ownBackground() {
		app.md.Theme.CodeBlockBackground = app.codeColors.Background
	}
}
