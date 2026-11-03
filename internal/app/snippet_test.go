package app

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
)

func firstKind(t *testing.T, tokens []editor.Token) editor.TokenKind {
	t.Helper()
	if len(tokens) == 0 {
		t.Fatal("line has no tokens")
	}
	return tokens[0].Kind
}

func TestSnippetOfAFileOutsideTheChangesTokenizesFromItsTop(t *testing.T) {
	app := &app{extra: map[string]*extraFile{
		"notes/a.go": newExtraFile("notes/a.go", "package a\n\n/*\nstill a comment\n*/\nvar x = 1\n", nil),
	}}
	snippet, ok := app.loadedSnippet(review.Ref{File: "notes/a.go", Side: review.SideNew, Start: 4, End: 6})
	if !ok {
		t.Fatal("snippet not loaded")
	}
	tokens := snippet.tokens()
	if kind := firstKind(t, tokens[0]); kind != editor.TokenComment {
		t.Errorf("line inside the comment above the snippet: kind %v, want comment", kind)
	}
	if kind := firstKind(t, tokens[2]); kind != editor.TokenKeyword {
		t.Errorf("var after the comment: kind %v, want keyword", kind)
	}
}

func TestSnippetOfAChangedFileUsesTheTextOfItsSide(t *testing.T) {
	app := newTestApp(t)
	change := app.diffs["cache/cache.go"].change
	app.setContents(change, "/*\nx := 1\n*/\n", "x := 1\nx := 1\n", nil)

	old, _ := app.loadedSnippet(review.Ref{File: "cache/cache.go", Side: review.SideOld, Start: 2, End: 2})
	if kind := firstKind(t, old.tokens()[0]); kind != editor.TokenComment {
		t.Errorf("old line 2: kind %v, want comment", kind)
	}
	updated, _ := app.loadedSnippet(review.Ref{File: "cache/cache.go", Side: review.SideNew, Start: 2, End: 2})
	if kind := firstKind(t, updated.tokens()[0]); kind == editor.TokenComment {
		t.Error("new line 2 is a comment, want code")
	}
}

func TestSavedSnippetIsHighlighted(t *testing.T) {
	snippet := savedSnippet(review.Ref{File: "a.go", Side: review.SideNew, Start: 3, End: 3, Code: []review.CodeLine{{Text: "func Get() {}"}}})
	if kind := firstKind(t, snippet.tokens()[0]); kind != editor.TokenKeyword {
		t.Errorf("func: kind %v, want keyword", kind)
	}
}

func TestSnippetOfAnUnknownLanguageIsPlain(t *testing.T) {
	app := &app{extra: map[string]*extraFile{"notes.unknown": newExtraFile("notes.unknown", "var x\n", nil)}}
	snippet, _ := app.loadedSnippet(review.Ref{File: "notes.unknown", Side: review.SideNew, Start: 1, End: 1})
	if tokens := snippet.tokens(); len(tokens) != 1 || tokens[0] != nil {
		t.Errorf("tokens %v, want one plain line", tokens)
	}
}

func TestCodeSpans(t *testing.T) {
	colors := &editor.Theme{}
	colors.Tokens[editor.TokenText] = editor.TokenStyle{Color: ui.Hex("#111111")}
	colors.Tokens[editor.TokenKeyword] = editor.TokenStyle{Color: ui.Hex("#ff0000"), Bold: true}
	colors.Tokens[editor.TokenComment] = editor.TokenStyle{Color: ui.Hex("#00ff00"), Italic: true}

	t.Run("colors each token and the text after the last one", func(t *testing.T) {
		got := codeSpans("var\tx // c", []editor.Token{{End: 3, Kind: editor.TokenKeyword}, {End: 4, Kind: editor.TokenText}, {End: 6, Kind: editor.TokenText}}, colors)
		want := []ui.Span{
			{Text: "var", Color: ui.Hex("#ff0000"), Weight: 700},
			{Text: "    ", Color: ui.Hex("#111111")},
			{Text: "x ", Color: ui.Hex("#111111")},
			{Text: "// c", Color: ui.Hex("#111111")},
		}
		assertSpans(t, got, want)
	})
	t.Run("cuts a token at the end of the line", func(t *testing.T) {
		got := codeSpans("/* a", []editor.Token{{End: 99, Kind: editor.TokenComment}}, colors)
		assertSpans(t, got, []ui.Span{{Text: "/* a", Color: ui.Hex("#00ff00"), Italic: true}})
	})
	t.Run("keeps one span for an empty line", func(t *testing.T) {
		assertSpans(t, codeSpans("", nil, colors), []ui.Span{{Text: "", Color: ui.Hex("#111111")}})
	})
}

func assertSpans(t *testing.T, got, want []ui.Span) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("spans %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("span %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSnippetCopiesItsCodeWithoutLineNumbers(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{ID: "ov", Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{Summary: "s", Steps: []review.Step{
		{ID: "s1", Title: "Lower-case keys", Text: "t", Refs: []review.Ref{{File: "cache/cache.go", Side: review.SideNew, Start: 5, End: 7}}},
	}}}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	first, ok := tt.Find("func Get(key string) string {")
	second, ok2 := tt.Find("    return strings.ToLower(key)")
	if !ok || !ok2 {
		t.Fatalf("snippet lines not shown: %q", tt.Texts())
	}
	tt.Press(first.X+1, first.Y+first.H/2)
	tt.Move(second.X+second.W-1, second.Y+second.H/2)
	tt.Release(second.X+second.W-1, second.Y+second.H/2)
	tt.Key(ui.Cmd, ui.KeyC)
	if got, want := tt.Clipboard(), "func Get(key string) string {\n    return strings.ToLower(key)"; got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
}

func TestSnippetLineKeepsTheDiscussMenuOfItsStep(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{ID: "ov", Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{Summary: "s", Steps: []review.Step{
		{ID: "s1", Title: "Lower-case keys", Text: "t", Refs: []review.Ref{{File: "cache/cache.go", Side: review.SideNew, Start: 5, End: 7}}},
	}}}
	tt := ui.NewTester(app.view, 1300, 800)
	tt.Frame()
	if err := tt.RightClick("func Get(key string) string {"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Menu(); !slices.Contains(got, "Copy") {
		t.Errorf("menu %q, want Copy", got)
	}
	if err := tt.ChooseMenuItem("Discuss"); err != nil {
		t.Fatalf("%v; menu %q", err, tt.Menu())
	}
	tt.Frame()
	th := app.rev.Thread(app.threadID())
	if th == nil || th.Anchor.Kind != review.AnchorSection || th.Anchor.Section != "Lower-case keys" {
		t.Errorf("thread = %+v", th)
	}
}
