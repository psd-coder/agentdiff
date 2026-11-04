package app

import (
	"testing"
	"time"

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

// TestComponentGallery builds every component on one page, in the light
// and the dark theme. With AGENTDIFF_SNAPSHOTS it writes the pages as
// gallery-light.png and gallery-dark.png.
func TestComponentGallery(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tt := ui.NewTester(gallery, 900, 1180)
		tt.SetDark(dark)
		tt.Frame()
		for _, want := range []string{"Page title", "Card title", "A link row", "The agent failed", "+12"} {
			if !tt.HasText(want) {
				t.Errorf("dark %v: the gallery lacks %q", dark, want)
			}
		}
		name := "gallery-light"
		if dark {
			name = "gallery-dark"
		}
		snapshot(t, tt, name)
	}
}

func gallery(ctx *ui.Context) {
	theme := ctx.Theme()
	section := func(title string, fn func()) {
		ui.Column(ctx.Key(title)).Gap(theme.Space(2)).Children(func() {
			smallLabel(ctx, title)
			fn()
		})
	}
	row := func(fn func()) {
		ui.Row(ctx).Gap(theme.Space(3)).AlignItems(ui.Center).Wrap().Children(fn)
	}
	ui.Scroll(ctx).Fill().Background(theme.Background).Children(func() {
		pageColumn(ctx).Gap(theme.Space(6)).Children(func() {
			section("Text", func() {
				pageTitle(ctx, "Page title")
				heading(ctx, "Heading")
				cardTitle(ctx, "Card title")
				leadText(ctx, "Lead text: the summary under a title.")
				ui.Text(ctx, "Body text in the theme's font size.")
				caption(ctx, "Caption: a note or a meta line")
				smallCaption(ctx, "Small caption: 14:05 · 3 files")
				codeText(ctx, "internal/app/components.go")
				row(func() {
					anchorLabel(ctx, "view.go:120")
					textLink(ctx, "Text link", func(ui.Element) {})
					errorText(ctx, "Error text")
					errorText(ctx, "Stopped")
				})
			})
			section("Messages", func() {
				messageHeader(ctx, "You", time.Date(2026, 11, 4, 14, 5, 0, 0, time.UTC))
				bubble(ctx, "A message the user wrote.")
				codeQuote(ctx, "func main() {\n\tfmt.Println(\"hi\")\n}").FontSize(fontSmall)
				activityLine(ctx, "Writing the overview…")
			})
			section("Containers", func() {
				card(ctx).Gap(theme.Space(2)).Children(func() { cardTitle(ctx, "card"); caption(ctx, "A main part of a page") })
				outlineCard(ctx).Gap(theme.Space(2)).Children(func() { ui.Text(ctx, "outlineCard").Bold(); caption(ctx, "A smaller part") })
				linkRow(ctx).Padding(theme.Space(2), theme.Space(3)).Children(func() { ui.Text(ctx, "A link row") })
				linkCard(ctx).Children(func() { ui.Text(ctx, "A link card").Bold(); caption(ctx, "An excerpt · a meta line") })
				warningBox(ctx).Children(func() { ui.Text(ctx, "A warning box") })
				failureBox(ctx, "exit status 1")
			})
			section("Controls", func() {
				row(func() {
					ui.Button(ctx, "Button")
					ui.PrimaryButton(ctx, "Primary")
					ghostButton(ctx).Label("Ghost").Children(func() { disclosureArrow(ctx, true) })
					ghostButton(ctx).Label("Folded").Children(func() { disclosureArrow(ctx, false) })
					iconButton(ctx, trashIcon, "Delete", "Delete")
					iconButton(ctx, trashIcon, "Delete beside a field", "Delete").Padding(theme.Space(2))
				})
			})
			section("Marks", func() {
				row(func() {
					diffStat(ctx, 12, 3).FontSize(fontTiny)
					diffStat(ctx, 7, 0).FontSize(fontSmall)
					badge(ctx, "new")
					countBadge(ctx, 4)
				})
				ui.Row(ctx).Gap(theme.Space(2)).Padding(theme.Space(1.5), theme.Space(2.5)).Radius(theme.Radius).Background(theme.Accent).Children(func() {
					ui.Text(ctx, "A chosen row").TextColor(theme.AccentText).Grow(1)
					rowEnd(ctx, true, func() {
						diffStat(ctx, 12, 3).FontSize(fontTiny)
						countBadge(ctx, 4)
					})
				})
				noMatch(ctx, "files")
			})
			section("Panel bars", func() {
				panelBar(ctx).Children(func() { ui.Text(ctx, "panelBar").Bold() })
				panelFooter(ctx).Children(func() { caption(ctx, "panelFooter") })
			})
		})
	})
}
