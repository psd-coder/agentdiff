package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

// kindAt tokenizes lines from the start of a file and returns the kind of
// the token that covers the first occurrence of word on line lineNo.
func kindAt(t *testing.T, lang *editor.Language, lines []string, lineNo int, word string) editor.TokenKind {
	t.Helper()
	var state uint64
	for i, line := range lines {
		var tokens []editor.Token
		tokens, state = lang.Tokenizer.Tokenize(line, state, nil)
		if i != lineNo {
			continue
		}
		at := strings.Index(line, word)
		if at < 0 {
			t.Fatalf("line %d %q has no %q", lineNo, line, word)
		}
		for _, tok := range tokens {
			if at < tok.End {
				return tok.Kind
			}
		}
		return editor.TokenText
	}
	t.Fatalf("no line %d", lineNo)
	return 0
}

func TestAstroHighlightsFrontmatterAsTypeScript(t *testing.T) {
	lines := []string{
		"---",
		"const title = 'Home'",
		"---",
		`<h1 class="big">{title}</h1>`,
	}
	lang := languageFor("src/pages/index.astro")
	if got := kindAt(t, lang, lines, 1, "const"); got != editor.TokenKeyword {
		t.Errorf("const in frontmatter: kind %v, want keyword", got)
	}
	if got := kindAt(t, lang, lines, 1, "'Home'"); got != editor.TokenString {
		t.Errorf("string in frontmatter: kind %v, want string", got)
	}
	if got := kindAt(t, lang, lines, 3, "h1"); got != editor.TokenTag {
		t.Errorf("h1 after frontmatter: kind %v, want tag", got)
	}
	if got := kindAt(t, lang, lines, 3, "class"); got != editor.TokenAttribute {
		t.Errorf("class after frontmatter: kind %v, want attribute", got)
	}
}

func TestAstroWithoutFrontmatterIsHTML(t *testing.T) {
	lines := []string{
		"<p>const</p>",
		"---",
		"<p>const</p>",
	}
	lang := languageFor("Card.ASTRO")
	if got := kindAt(t, lang, lines, 0, "p"); got != editor.TokenTag {
		t.Errorf("p on first line: kind %v, want tag", got)
	}
	if got := kindAt(t, lang, lines, 2, "const"); got == editor.TokenKeyword {
		t.Error("--- inside the template started frontmatter")
	}
}

func TestLanguageForOtherFiles(t *testing.T) {
	if got := languageFor("main.go"); got == nil || got.Name != "Go" {
		t.Errorf("main.go: %v, want Go", got)
	}
	if got := languageFor("notes.unknown"); got != nil {
		t.Errorf("notes.unknown: %v, want nil", got.Name)
	}
}
