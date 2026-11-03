package app

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

// languageFor returns the language that highlights a file in the diff.
func languageFor(file string) *editor.Language {
	if strings.EqualFold(path.Ext(file), ".astro") {
		return astroLanguage
	}
	return editor.LanguageFor(file)
}

var astroLanguage = func() *editor.Language {
	html := editor.LanguageNamed("html")
	astro := *html
	astro.Name = "Astro"
	astro.Extensions = []string{".astro"}
	astro.Tokenizer = astroTokenizer{
		frontmatter: editor.LanguageNamed("typescript").Tokenizer,
		template:    html.Tokenizer,
	}
	return &astro
}()

// The inner tokenizers keep their state below bit 56, so the two top bits
// are free for the part of the file a line is in. A state without either
// bit is the start of the file.
const (
	astroInFrontmatter uint64 = 1 << 63
	astroInTemplate    uint64 = 1 << 62
	astroInner                = astroInTemplate - 1
)

// astroTokenizer highlights an Astro component: the TypeScript between the
// --- fences at the top as TypeScript, and the template after it as HTML.
type astroTokenizer struct {
	frontmatter, template editor.Tokenizer
}

func (astro astroTokenizer) Tokenize(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
	fence := strings.TrimRight(line, " \t\r") == "---"
	switch {
	case state&astroInFrontmatter != 0 && fence:
		return append(tokens, editor.Token{End: len(line), Kind: editor.TokenPunctuation}), astroInTemplate
	case state&astroInFrontmatter != 0:
		tokens, inner := astro.frontmatter.Tokenize(line, state&astroInner, tokens)
		return tokens, astroInFrontmatter | inner
	case state == 0 && fence:
		return append(tokens, editor.Token{End: len(line), Kind: editor.TokenPunctuation}), astroInFrontmatter
	}
	tokens, inner := astro.template.Tokenize(line, state&astroInner, tokens)
	return tokens, astroInTemplate | inner
}
