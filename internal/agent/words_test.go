package agent

import (
	"slices"
	"testing"
)

func TestSplitWords(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{"", nil},
		{"  claude  ", []string{"claude"}},
		{"dev  claude", []string{"dev", "claude"}},
		{`"/Applications/My Tools/claude"`, []string{"/Applications/My Tools/claude"}},
		{`'it''s'`, []string{"its"}},
		{`'a\b' "c\"d" e\ f`, []string{`a\b`, `c"d`, "e f"}},
		{`""`, []string{""}},
		{`dev "un closed`, []string{"dev", "un closed"}},
	}
	for _, tt := range tests {
		if got := SplitWords(tt.line); !slices.Equal(got, tt.want) {
			t.Errorf("SplitWords(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestJoinWordsQuotesWhatSplitWordsSplits(t *testing.T) {
	words := []string{"~/bin/dev", "exec", "/Applications/My Tools/it's", ""}
	line := JoinWords(words)
	if want := `~/bin/dev exec '/Applications/My Tools/it'\''s' ''`; line != want {
		t.Errorf("JoinWords = %s, want %s", line, want)
	}
	if got := SplitWords(line); !slices.Equal(got, words) {
		t.Errorf("SplitWords(JoinWords) = %q, want %q", got, words)
	}
}
