package app

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
}

// pickRow adds a row to a pickList: body, and actions after it that show
// while the highlight is on the row. A click on the row chooses it.
type pickRow func(ctx *ui.Context, body func(), actions func()) ui.Element

// open puts the highlight on row i, as the list opens or its filter
// changes.
func (pickList *pickList) open(row int) { pickList.hi, pickList.reveal = max(row, 0), true }

// key moves the highlight among n rows and reports whether it took k.
func (pickList *pickList) key(key ui.Key, rowCount int) bool {
	hi := pickList.hi
	switch key {
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
	pickList.hi, pickList.reveal = max(0, min(hi, rowCount-1)), true
	return true
}

// list builds the rows that fn adds with addRow, under label for assistive
// technology. It returns the list and its rows, which last for this
// build pass only.
func (pickList *pickList) list(ctx *ui.Context, label string, fn func(addRow pickRow)) (ui.Element, []ui.Element) {
	var rows, bars []ui.Element
	list := ui.Column(ctx).Gap(2).Role(ui.RoleList).Label(label).KeepFocus()
	// In window coordinates, so that the list scrolling under a resting
	// pointer does not count as the pointer moving.
	pointerX, pointerY, over := list.PointerPosition()
	wx, wy := list.LocalToWindow(pointerX, pointerY)
	pt := [2]float32{wx, wy}
	pickList.follow = over && pt != pickList.pointer
	if over {
		pickList.pointer = pt
	}
	addRow := func(c *ui.Context, body func(), actions func()) ui.Element {
		theme := c.Theme()
		row := ui.Row(c).AlignItems(ui.Center).Radius(theme.Radius).Role(ui.RoleListItem)
		if pickList.follow && row.Hovered() {
			pickList.hi = len(rows)
		}
		var bar ui.Element
		row.Children(func() {
			ui.Row(c).Grow(1).Shrink(1).Gap(menuGap).Padding(6, menuInset).AlignItems(ui.Center).Children(body)
			if actions != nil {
				bar = ui.Row(c).Gap(2).PaddingEnd(4).Children(actions)
			}
		})
		rows, bars = append(rows, row), append(bars, bar)
		return row
	}
	list.Children(func() { fn(addRow) })
	pickList.hi = max(0, min(pickList.hi, len(rows)-1))
	for i, row := range rows {
		on := i == pickList.hi
		row.Checked(on)
		if on {
			row.Background(ctx.Theme().Surface)
			if pickList.reveal {
				row.ScrollIntoView()
			}
		}
		if bar := bars[i]; !on && bar != (ui.Element{}) {
			bar.Invisible()
		}
	}
	pickList.reveal = false
	return list, rows
}

// active is the row of rows the highlight is on, which assistive
// technology follows as the focused one.
func (pickList *pickList) active(rows []ui.Element) (ui.Element, bool) {
	if pickList.hi < len(rows) {
		return rows[pickList.hi], true
	}
	return ui.Element{}, false
}

// searchField is the field filtering a pickList, which takes the keys
// onKey does not.
func searchField(ctx *ui.Context, query *string, label string, onKey func(key ui.TextKey) bool) ui.Element {
	theme := ctx.Theme()
	var in ui.Element
	field := ui.Row(ctx).AlignItems(ui.Center).Gap(menuGap).Padding(6, menuInset-1).Radius(theme.Radius).Background(theme.Surface).Border(1, theme.Border).Cursor(ui.CursorText)
	field.Children(func() {
		ui.Icon(ctx, searchIcon).Size(menuIcon, menuIcon).TextColor(theme.TextMuted)
		in = ui.TextInputBase(ctx, query).Grow(1).Placeholder(label).Label(label).AutoFocus().OnTextKey(onKey)
	})
	if in.Focused() {
		field.BorderColor(theme.Accent)
	}
	if field.Clicked() {
		in.Focus()
	}
	return in
}

// pickerButton is the face of a button that opens a list below it.
func pickerButton(ctx *ui.Context, svg *ui.SVG, label, detail string, open bool) ui.Element {
	theme := ctx.Theme()
	return ui.Button(ctx, "").Role(ui.RolePopUpButton).Expanded(open).Children(func() {
		ui.Icon(ctx, svg).TextColor(theme.TextMuted)
		ui.Text(ctx, label).Bold().SingleLine()
		if detail != "" {
			ui.Text(ctx, detail).TextColor(theme.TextMuted).SingleLine()
		}
		ui.Icon(ctx, chevronIcon).FontSize(12).TextColor(theme.TextMuted)
	})
}

// iconButton is a button showing an icon, named label for assistive
// technology, with the tooltip tip.
func iconButton(ctx *ui.Context, svg *ui.SVG, label, tip string) ui.Element {
	return ui.Button(ctx, "").Padding(4, 6).Label(label).Tooltip(tip).Children(func() {
		ui.Icon(ctx, svg).FontSize(13)
	})
}

// textLink is a text that leads to another place in the app. It takes
// the focus with Tab, and Enter or Space follows it, as a click does.
// style styles the text.
func textLink(ctx *ui.Context, label string, style func(text ui.Element)) ui.Element {
	theme := ctx.Theme()
	return ui.ButtonBase(ctx).Role(ui.RoleLink).Cursor(ui.CursorPointer).Shrink(1).Radius(4).Children(func() {
		text := ui.Text(ctx, label).TextColor(theme.Accent).SingleLine()
		style(text)
	})
}
