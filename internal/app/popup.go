package app

import "github.com/egoist/mygo/ui"

// popUp is the drop-down of the app, in place of a plain ui.Select: it
// chooses one of options into *selected, and it is as wide as the chosen
// option and its arrow, as the pop-up buttons of macOS. It keeps to the
// start of its row, so a form field does not stretch it.
func popUp(ctx *ui.Context, selected *string, options []string) ui.Element {
	return ui.Select(ctx, selected, options).AlignSelf(ui.Start).MinWidth(0)
}
