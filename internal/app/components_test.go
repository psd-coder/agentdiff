package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestStatColors darkens the counts of a diff stat in a light theme only,
// and keeps the white of a row in the accent color.
func TestStatColors(t *testing.T) {
	light, dark := ui.LightTheme(), ui.DarkTheme()
	onAccent := *light
	onAccent.Success, onAccent.Danger = light.AccentText, light.AccentText
	for _, c := range []struct {
		name           string
		theme          *ui.Theme
		added, deleted ui.Color
	}{
		{"light", light, ui.Hex("#166534"), ui.Hex("#b91c1c")},
		{"dark", dark, dark.Success, dark.Danger},
		{"accent row", &onAccent, light.AccentText, light.AccentText},
	} {
		if added, deleted := statColors(c.theme); added != c.added || deleted != c.deleted {
			t.Errorf("%s: %v and %v, want %v and %v", c.name, added, deleted, c.added, c.deleted)
		}
	}
}
