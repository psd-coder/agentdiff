package app

import "github.com/egoist/mygo/ui/editor"

// highlighterOf highlights the text of buf in its language, or is nil for
// a buffer without one.
func highlighterOf(buf *editor.Buffer) editor.Highlighter {
	lang := buf.Language()
	if lang == nil || lang.Tokenizer == nil {
		return nil
	}
	return editor.NewTokenHighlighter(lang.Tokenizer, buf.Snapshot())
}

// codeHighlighter highlights text in the language of the file at path.
func codeHighlighter(path, text string) editor.Highlighter {
	buf := editor.NewBuffer(text)
	buf.SetLanguage(languageFor(path))
	return highlighterOf(buf)
}
