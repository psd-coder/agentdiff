package agent

import "strings"

// SplitWords splits a command line into its words as a shell does with
// quotes and backslashes, without expanding anything. A quote that is
// not closed runs to the end.
func SplitWords(line string) []string {
	var words []string
	var word strings.Builder
	inWord, quote, escaped := false, rune(0), false
	for _, char := range line {
		switch {
		case escaped:
			word.WriteRune(char)
			escaped = false
		case char == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(char)
		case char == '\'' || char == '"':
			quote, inWord = char, true
		case char == ' ' || char == '\t' || char == '\n':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(char)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// JoinWords is the command line that SplitWords splits into words. It
// quotes only the words that SplitWords would split or change, so a path
// as ~/bin/claude stays as the user wrote it.
func JoinWords(words []string) string {
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = word
		if word == "" || strings.ContainsAny(word, " \t\n'\"\\") {
			quoted[i] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}
