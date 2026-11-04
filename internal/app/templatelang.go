package app

import (
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

// componentLanguage is the language of single-file components, as Vue's
// and Svelte's: HTML whose scripts, styles and expressions the template
// tokenizer highlights.
func componentLanguage(name, ext string, template componentTokenizer) *editor.Language {
	lang := *editor.LanguageNamed("html")
	lang.Name = name
	lang.Extensions = []string{ext}
	lang.Tokenizer = template.with()
	return &lang
}

// componentTokenizer highlights the template of a component. MyGo's XML
// tokenizer lexes the markup, because it leaves the text of <script> and
// <style> alone: this tokenizer highlights that text itself, as TypeScript
// when the script says lang="ts", and the expressions in the text between
// tags.
type componentTokenizer struct {
	// braces open an expression in the text, as "{{" in Vue and "{" in
	// Svelte and Astro.
	braces string
	// tsScripts makes a <script> without lang TypeScript, as in Astro.
	tsScripts bool

	markup, script, typescript, style editor.Tokenizer
}

// with returns the tokenizer with the tokenizers of the languages it
// holds.
func (template componentTokenizer) with() componentTokenizer {
	template.markup = editor.LanguageNamed("xml").Tokenizer
	template.script = editor.LanguageNamed("javascript").Tokenizer
	template.typescript = editor.LanguageNamed("typescript").Tokenizer
	template.style = editor.LanguageNamed("css").Tokenizer
	return template
}

// The state of a line keeps the state of the tokenizer of its region in
// bits 0-39, which the JavaScript, TypeScript, CSS and XML tokenizers fit
// in, the open tag that starts a region in bits 40-44, and the region in
// bits 48-49. The Astro tokenizer keeps bits 62 and 63.
const (
	innerBits    uint64 = 1<<40 - 1
	pendingShift        = 40
	regionShift         = 48
)

type region uint64

const (
	inMarkup region = iota
	inScript
	inTypeScript
	inStyle
)

// openTag is a <script> or <style> tag whose > has not come yet, with
// what its lang attribute says so far.
type openTag struct {
	tag  region // inScript or inStyle, or inMarkup for none
	lang uint8  // 1 after lang, 2 after lang=
	ts   bool
}

func (open openTag) pack() uint64 {
	out := uint64(open.tag) | uint64(open.lang)<<2
	if open.ts {
		out |= 1 << 4
	}
	return out
}

func unpackOpenTag(bits uint64) openTag {
	return openTag{tag: region(bits & 3), lang: uint8(bits >> 2 & 3), ts: bits>>4&1 != 0}
}

func (template componentTokenizer) Tokenize(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
	reg := region(state >> regionShift & 3)
	inner := state & innerBits
	open := unpackOpenTag(state >> pendingShift & 0x1f)
	at := 0
	for {
		if reg != inMarkup {
			var closing string
			var embedded editor.Tokenizer
			switch reg {
			case inStyle:
				closing, embedded = "</style", template.style
			case inTypeScript:
				closing, embedded = "</script", template.typescript
			default:
				closing, embedded = "</script", template.script
			}
			end := indexASCIIFold(line[at:], closing)
			stop := len(line)
			if end >= 0 {
				stop = at + end
			}
			var toks []editor.Token
			toks, inner = embedded.Tokenize(line[at:stop], inner, nil)
			tokens = appendShifted(tokens, toks, at, stop)
			if end < 0 {
				return tokens, uint64(reg)<<regionShift | inner&innerBits
			}
			reg, inner, at = inMarkup, 0, stop
			continue
		}
		toks, next := template.markup.Tokenize(line[at:], inner, nil)
		cut, opened := open.scan(line[at:], toks, template.tsScripts)
		if cut < 0 {
			tokens = template.withExpressions(line, tokens, toks, at)
			return tokens, next&innerBits | open.pack()<<pendingShift
		}
		toks = clipTokens(toks, cut)
		tokens = template.withExpressions(line, tokens, toks, at)
		reg, inner, open, at = opened, 0, openTag{}, at+cut
	}
}

// scan follows the tags in the tokens of a line of markup. It returns
// where the > that opens a script or a style ends, and the region it
// opens, or -1 when the line opens none.
func (open *openTag) scan(line string, toks []editor.Token, tsScripts bool) (int, region) {
	start := 0
	for _, tok := range toks {
		text := line[start:tok.End]
		switch tok.Kind {
		case editor.TokenTag:
			*open = openTag{}
			if start > 0 && line[start-1] == '<' {
				switch strings.ToLower(text) {
				case "script":
					*open = openTag{tag: inScript, ts: tsScripts}
				case "style":
					*open = openTag{tag: inStyle}
				}
			}
		case editor.TokenAttribute, editor.TokenString:
			switch {
			case open.tag == inMarkup:
			case open.lang == 2:
				value := strings.ToLower(strings.Trim(text, `"'`))
				open.ts, open.lang = value == "ts" || value == "typescript", 0
			case tok.Kind == editor.TokenAttribute:
				open.lang = 0
				if strings.EqualFold(text, "lang") {
					open.lang = 1
				}
			}
		case editor.TokenOperator:
			if open.lang == 1 && text == "=" {
				open.lang = 2
			}
		case editor.TokenPunctuation:
			if open.tag == inMarkup {
				break
			}
			if gt := strings.IndexByte(text, '>'); gt >= 0 {
				if gt > 0 && text[gt-1] == '/' {
					*open = openTag{}
					break
				}
				opened := open.tag
				if opened == inScript && open.ts {
					opened = inTypeScript
				}
				return start + gt + 1, opened
			}
		default:
		}
		start = tok.End
	}
	return -1, inMarkup
}

// withExpressions appends the tokens of markup that starts at byte at of
// line, with the expressions in its text highlighted.
func (template componentTokenizer) withExpressions(line string, tokens, toks []editor.Token, at int) []editor.Token {
	start := at
	for _, tok := range toks {
		end := at + tok.End
		if tok.Kind == editor.TokenText {
			tokens = template.expressions(line, start, end, tokens)
		} else {
			tokens = add(tokens, end, tok.Kind)
		}
		start = end
	}
	return tokens
}

// expressions appends the tokens of the text from start to end of line:
// TypeScript in the expressions that close on the line, and text around
// them.
func (template componentTokenizer) expressions(line string, start, end int, tokens []editor.Token) []editor.Token {
	open, closing := template.braces, strings.Repeat("}", len(template.braces))
	for start < end {
		at := strings.Index(line[start:end], open)
		if at < 0 {
			break
		}
		from := start + at + len(open)
		to := closingBrace(line[from:end])
		if to < 0 || !strings.HasPrefix(line[from+to:end], closing) {
			break
		}
		to += from
		tokens = add(tokens, from-len(open), editor.TokenText)
		tokens = add(tokens, from, editor.TokenPunctuation)
		body := from + blockWord(line[from:to])
		tokens = add(tokens, body, editor.TokenKeyword)
		toks, _ := template.typescript.Tokenize(line[body:to], 0, nil)
		tokens = appendShifted(tokens, toks, body, to)
		start = to + len(closing)
		tokens = add(tokens, start, editor.TokenPunctuation)
	}
	return add(tokens, end, editor.TokenText)
}

// closingBrace returns where the expression at the start of text ends:
// the first closing brace outside the braces and strings in it, or -1
// when it does not end in text.
func closingBrace(text string) int {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch char := text[i]; char {
		case '"', '\'', '`':
			end := strings.IndexByte(text[i+1:], char)
			if end < 0 {
				return -1
			}
			i += end + 1
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// blockWord returns the length of the Svelte block word at the start of
// an expression, as #if, :else or /each, or 0 for none.
func blockWord(expr string) int {
	if len(expr) < 2 || strings.IndexByte("#:/@", expr[0]) < 0 || !isLetter(expr[1]) {
		return 0
	}
	size := 1
	for size < len(expr) && isLetter(expr[size]) {
		size++
	}
	return size
}

func isLetter(char byte) bool { return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' }

// appendShifted appends the tokens of the text from at to end of a line,
// whose offsets start at at, and covers that text to its end.
func appendShifted(tokens, toks []editor.Token, at, end int) []editor.Token {
	for _, tok := range toks {
		tokens = add(tokens, at+tok.End, tok.Kind)
	}
	return add(tokens, end, editor.TokenText)
}

// add appends a token that ends at end, unless the tokens already reach
// it, and grows the last token when it is of the same kind.
func add(tokens []editor.Token, end int, kind editor.TokenKind) []editor.Token {
	if end <= 0 {
		return tokens
	}
	if count := len(tokens); count > 0 {
		last := &tokens[count-1]
		if last.End >= end {
			return tokens
		}
		if last.Kind == kind {
			last.End = end
			return tokens
		}
	}
	return append(tokens, editor.Token{End: end, Kind: kind})
}

// clipTokens cuts the tokens at byte cut.
func clipTokens(toks []editor.Token, cut int) []editor.Token {
	for i, tok := range toks {
		if tok.End >= cut {
			toks = toks[:i+1]
			toks[i].End = cut
			return toks
		}
	}
	return toks
}

// indexASCIIFold returns where lower, in lower case, first is in text,
// ignoring the case of the ASCII letters of text, or -1.
func indexASCIIFold(text, lower string) int {
	for i := 0; i+len(lower) <= len(text); i++ {
		match := true
		for j := 0; j < len(lower) && match; j++ {
			char := text[i+j]
			if char >= 'A' && char <= 'Z' {
				char += 'a' - 'A'
			}
			match = char == lower[j]
		}
		if match {
			return i
		}
	}
	return -1
}
