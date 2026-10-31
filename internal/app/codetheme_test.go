package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestCodeThemeFollowsTheWindow(t *testing.T) {
	win := ui.DarkTheme()
	got := codeThemeNamed("One").colors(win)
	if got.Background != win.Background {
		t.Errorf("background = %v, want the window's %v", got.Background, win.Background)
	}
	if want := ui.Hex("#c678dd"); got.Tokens[editor.TokenKeyword].Color != want {
		t.Errorf("keyword = %v, want One Dark's %v", got.Tokens[editor.TokenKeyword].Color, want)
	}
	if got.Tokens[editor.TokenText].Color != win.Text {
		t.Errorf("a token the theme leaves out = %v, want the window's text %v", got.Tokens[editor.TokenText].Color, win.Text)
	}

	light := codeThemeNamed("One").colors(ui.LightTheme())
	if want := ui.Hex("#a626a4"); light.Tokens[editor.TokenKeyword].Color != want {
		t.Errorf("keyword in light = %v, want One Light's %v", light.Tokens[editor.TokenKeyword].Color, want)
	}
}

func TestDarkOnlyCodeThemeBringsItsBackground(t *testing.T) {
	got := codeThemeNamed("Dracula").colors(ui.LightTheme())
	if want := ui.Hex("#282a36"); got.Background != want {
		t.Errorf("background = %v, want %v", got.Background, want)
	}
	if want := ui.Hex("#f8f8f2"); got.Tokens[editor.TokenText].Color != want {
		t.Errorf("text = %v, want %v", got.Tokens[editor.TokenText].Color, want)
	}
}

func TestCodeThemeKeepsMarkupStyles(t *testing.T) {
	got := codeThemeNamed("One").colors(ui.DarkTheme())
	if !got.Tokens[editor.TokenEmphasis].Italic || !got.Tokens[editor.TokenStrong].Bold {
		t.Errorf("emphasis italic = %v, strong bold = %v", got.Tokens[editor.TokenEmphasis].Italic, got.Tokens[editor.TokenStrong].Bold)
	}
}

func TestCodeThemeNamedFallsBackToGitHub(t *testing.T) {
	for _, name := range []string{"", "Sepia"} {
		if got := codeThemeNamed(name).name; got != "GitHub" {
			t.Errorf("codeThemeNamed(%q) = %q, want GitHub", name, got)
		}
	}
}

func TestCodeThemeSetting(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1300, 800)
	a.openSettings()
	tt.Frame()
	a.settings.codeTheme = "Dracula"
	tt.Frame()
	if got := a.store.LoadConfig().CodeTheme; got != "Dracula" {
		t.Errorf("saved code theme = %q, want Dracula", got)
	}
	tt.Frame()
	want := ui.Hex("#282a36")
	if a.codeColors.Background != want || a.md.Theme.CodeBlockBackground != want {
		t.Errorf("diff background %v, code block %v, want %v", a.codeColors.Background, a.md.Theme.CodeBlockBackground, want)
	}

	a.settings.codeTheme = "GitHub"
	tt.Frame()
	if got := a.store.LoadConfig().CodeTheme; got != "" {
		t.Errorf("saved code theme = %q, want the default", got)
	}
}
