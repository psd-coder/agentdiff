package main

import "github.com/egoist/mygo/ui"

// The insets of a picker row line its icon and text up with those of
// the search field above the list, which has a 15 DIP magnifier.
const (
	menuInset = 9
	menuGap   = 7
	menuIcon  = 15
)

// pickList is a list in a dropdown that works as the options of a
// combobox do: Up and Down move a highlight while the keyboard focus stays
// on the search field, or on the list without one, Enter chooses the row
// it is on, and the pointer moves it as it moves over the rows.
type pickList struct {
	hi      int
	reveal  bool
	pointer [2]float32
	follow  bool
	rows    []ui.Element
	bars    []ui.Element
}

// open puts the highlight on row i, as the list opens or its filter
// changes.
func (p *pickList) open(i int) { p.hi, p.reveal = max(i, 0), true }

// key moves the highlight among n rows and reports whether it took k.
func (p *pickList) key(k ui.Key, n int) bool {
	hi := p.hi
	switch k {
	case ui.KeyDown:
		hi++
	case ui.KeyUp:
		hi--
	case ui.KeyPageDown:
		hi += 10
	case ui.KeyPageUp:
		hi -= 10
	default:
		return false
	}
	p.hi, p.reveal = max(0, min(hi, n-1)), true
	return true
}

// list builds the rows that fn adds with row, under label for assistive
// technology.
func (p *pickList) list(c *ui.Context, label string, fn func()) ui.Element {
	p.rows, p.bars = p.rows[:0], p.bars[:0]
	l := ui.Column(c).Gap(2).Role(ui.RoleList).Label(label).KeepFocus()
	// In window coordinates, so that the list scrolling under a resting
	// pointer does not count as the pointer moving.
	x, y, over := l.PointerPosition()
	wx, wy := l.LocalToWindow(x, y)
	pt := [2]float32{wx, wy}
	p.follow = over && pt != p.pointer
	if over {
		p.pointer = pt
	}
	l.Children(fn)
	p.hi = max(0, min(p.hi, len(p.rows)-1))
	for i, row := range p.rows {
		on := i == p.hi
		row.Checked(on)
		if on {
			row.Background(c.Theme().Surface)
			if p.reveal {
				row.ScrollIntoView()
			}
		}
		if bar := p.bars[i]; !on && bar != (ui.Element{}) {
			bar.Invisible()
		}
	}
	p.reveal = false
	return l
}

// row adds a row: body, and actions after it that show while the
// highlight is on the row. A click on the row chooses it.
func (p *pickList) row(c *ui.Context, body func(), actions func()) ui.Element {
	t := c.Theme()
	row := ui.Row(c).AlignItems(ui.Center).Radius(t.Radius).Role(ui.RoleListItem)
	if p.follow && row.Hovered() {
		p.hi = len(p.rows)
	}
	var bar ui.Element
	row.Children(func() {
		ui.Row(c).Grow(1).Shrink(1).Gap(menuGap).Padding(6, menuInset).AlignItems(ui.Center).Children(body)
		if actions != nil {
			bar = ui.Row(c).Gap(2).PaddingEnd(4).Children(actions)
		}
	})
	p.rows, p.bars = append(p.rows, row), append(p.bars, bar)
	return row
}

// active is the row the highlight is on, which assistive technology
// follows as the focused one.
func (p *pickList) active() (ui.Element, bool) {
	if p.hi < len(p.rows) {
		return p.rows[p.hi], true
	}
	return ui.Element{}, false
}

// searchField is the field filtering a pickList, which takes the keys
// onKey does not.
func searchField(c *ui.Context, query *string, label string, onKey func(k ui.TextKey) bool) ui.Element {
	t := c.Theme()
	var in ui.Element
	f := ui.Row(c).AlignItems(ui.Center).Gap(menuGap).Padding(6, menuInset-1).Radius(t.Radius).Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorText)
	f.Children(func() {
		ui.Icon(c, searchIcon).Size(menuIcon, menuIcon).TextColor(t.TextMuted)
		in = ui.TextInputBase(c, query).Grow(1).Placeholder(label).Label(label).AutoFocus().OnTextKey(onKey)
	})
	if in.Focused() {
		f.BorderColor(t.Accent)
	}
	if f.Clicked() {
		in.Focus()
	}
	return in
}

// pickerButton is the face of a button that opens a list below it.
func pickerButton(c *ui.Context, svg *ui.SVG, label, detail string, open bool) ui.Element {
	t := c.Theme()
	return ui.Button(c, "").Role(ui.RolePopUpButton).Expanded(open).Children(func() {
		ui.Icon(c, svg).TextColor(t.TextMuted)
		ui.Text(c, label).Bold().SingleLine()
		if detail != "" {
			ui.Text(c, detail).TextColor(t.TextMuted).SingleLine()
		}
		ui.Icon(c, chevronIcon).FontSize(12).TextColor(t.TextMuted)
	})
}

func iconButton(c *ui.Context, svg *ui.SVG, label string) ui.Element {
	return ui.Button(c, "").Padding(4, 6).Label(label).Tooltip(label).Children(func() {
		ui.Icon(c, svg).FontSize(13)
	})
}

// textLink is a text that leads to another place in the app. It takes
// the focus with Tab, and Enter or Space follows it, as a click does.
// style styles the text.
func textLink(c *ui.Context, label string, style func(text ui.Element)) ui.Element {
	t := c.Theme()
	return ui.ButtonBase(c).Role(ui.RoleLink).Cursor(ui.CursorPointer).Shrink(1).Radius(4).Children(func() {
		text := ui.Text(c, label).TextColor(t.Accent).SingleLine()
		style(text)
	})
}
