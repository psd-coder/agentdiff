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
func (ct codeTheme) colors(t *ui.Theme) *editor.Theme {
	v := ct.light
	if t.Dark && ct.dark != nil || v == nil {
		v = ct.dark
	}
	if v == nil {
		return editor.DefaultTheme(t)
	}
	base := t
	if ct.ownBackground() {
		base = ui.LightTheme()
		if v.dark {
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
		set(&out.Background, v.background)
		set(&out.Text, v.text)
		set(&out.LineNumber, v.lineNumber)
		set(&out.ActiveLineNumber, v.activeLineNumber)
		set(&out.Selection, v.selection)
		out.InactiveSelection = out.Selection.Alpha(0.55)
	}
	// A token the theme leaves out shows as text, as in VS Code.
	for k := range out.Tokens {
		out.Tokens[k].Color = out.Text
	}
	for k, tok := range v.tokens {
		s := &out.Tokens[k]
		s.Color = ui.Hex(tok.color)
		s.Italic = s.Italic || tok.italic
		s.Bold = s.Bold || tok.bold
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
func (a *app) syncCodeTheme(c *ui.Context) {
	key := codeThemeKey{name: a.cfg.CodeTheme, ui: *c.Theme()}
	if a.codeColors != nil && key == a.codeKey {
		return
	}
	ct := codeThemeNamed(a.cfg.CodeTheme)
	a.codeKey, a.codeColors = key, ct.colors(c.Theme())
	a.md.Theme = &markdown.Theme{CodeColors: a.codeColors}
	if ct.ownBackground() {
		a.md.Theme.CodeBlockBackground = a.codeColors.Background
	}
}
