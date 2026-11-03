package app

import (
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

func TestComponentLanguages(t *testing.T) {
	for file, want := range map[string]string{"App.vue": "Vue", "Card.Svelte": "Svelte", "index.astro": "Astro"} {
		if got := languageFor(file); got == nil || got.Name != want {
			t.Errorf("%s: %v, want %s", file, got, want)
		}
	}
}

func TestComponentScripts(t *testing.T) {
	vue := languageFor("App.vue")
	t.Run("lang ts makes the script TypeScript", func(t *testing.T) {
		lines := []string{`<script setup lang="ts">`, "interface Props {}", "</script>"}
		if kind := kindAt(t, vue, lines, 1, "interface"); kind != editor.TokenKeyword {
			t.Errorf("interface: kind %v, want keyword", kind)
		}
	})
	t.Run("lang typescript in single quotes makes the script TypeScript", func(t *testing.T) {
		lines := []string{`<script lang='typescript'>`, "interface Props {}"}
		if kind := kindAt(t, languageFor("Card.svelte"), lines, 1, "interface"); kind != editor.TokenKeyword {
			t.Errorf("interface: kind %v, want keyword", kind)
		}
	})
	t.Run("a script without lang is JavaScript", func(t *testing.T) {
		lines := []string{"<script>", "const props = {}", "interface Props {}"}
		if kind := kindAt(t, vue, lines, 1, "const"); kind != editor.TokenKeyword {
			t.Errorf("const: kind %v, want keyword", kind)
		}
		if kind := kindAt(t, vue, lines, 2, "interface"); kind == editor.TokenKeyword {
			t.Error("interface is a keyword in JavaScript")
		}
	})
	t.Run("lang js keeps an Astro script JavaScript", func(t *testing.T) {
		lines := []string{`<script lang="js">`, "interface Props {}"}
		if kind := kindAt(t, languageFor("a.astro"), lines, 1, "interface"); kind == editor.TokenKeyword {
			t.Error("interface is a keyword in JavaScript")
		}
	})
	t.Run("a script without lang is TypeScript in Astro", func(t *testing.T) {
		lines := []string{"<h1>Hi</h1>", "<script>", "interface Props {}"}
		if kind := kindAt(t, languageFor("a.astro"), lines, 2, "interface"); kind != editor.TokenKeyword {
			t.Errorf("interface: kind %v, want keyword", kind)
		}
	})
	t.Run("an open tag over several lines", func(t *testing.T) {
		lines := []string{"<script", "  setup", `  lang="ts"`, ">", "interface Props {}"}
		if kind := kindAt(t, vue, lines, 4, "interface"); kind != editor.TokenKeyword {
			t.Errorf("interface: kind %v, want keyword", kind)
		}
	})
	t.Run("markup goes on after the script closes", func(t *testing.T) {
		lines := []string{"<script>", "const a = 1</script><main>", "<footer>"}
		if kind := kindAt(t, vue, lines, 1, "main"); kind != editor.TokenTag {
			t.Errorf("main: kind %v, want tag", kind)
		}
		if kind := kindAt(t, vue, lines, 2, "footer"); kind != editor.TokenTag {
			t.Errorf("footer: kind %v, want tag", kind)
		}
	})
	t.Run("a self-closing script opens no script", func(t *testing.T) {
		lines := []string{`<script src="a.js" />`, "<main>"}
		if kind := kindAt(t, vue, lines, 1, "main"); kind != editor.TokenTag {
			t.Errorf("main: kind %v, want tag", kind)
		}
	})
}

func TestComponentStyles(t *testing.T) {
	for _, open := range []string{"<style>", `<style lang="scss">`} {
		lines := []string{open, ".title { color: red }", "</style>", "<main>"}
		if kind := kindAt(t, languageFor("App.vue"), lines, 1, "color"); kind != editor.TokenProperty {
			t.Errorf("%s: color has kind %v, want property", open, kind)
		}
		if kind := kindAt(t, languageFor("App.vue"), lines, 3, "main"); kind != editor.TokenTag {
			t.Errorf("%s: main after the style has kind %v, want tag", open, kind)
		}
	}
}

func TestComponentExpressions(t *testing.T) {
	vue, svelte := languageFor("App.vue"), languageFor("Card.svelte")
	t.Run("double braces in Vue", func(t *testing.T) {
		lines := []string{"<p>{{ count + 1 }} and { 2 }</p>"}
		if kind := kindAt(t, vue, lines, 0, "1"); kind != editor.TokenNumber {
			t.Errorf("1 in double braces: kind %v, want number", kind)
		}
		if kind := kindAt(t, vue, lines, 0, "2"); kind != editor.TokenText {
			t.Errorf("2 in single braces: kind %v, want text", kind)
		}
	})
	t.Run("braces inside a Vue expression", func(t *testing.T) {
		lines := []string{"<p>{{ format({ digits: 3 }) }} 4</p>"}
		if kind := kindAt(t, vue, lines, 0, "3"); kind != editor.TokenNumber {
			t.Errorf("3: kind %v, want number", kind)
		}
		if kind := kindAt(t, vue, lines, 0, "4"); kind != editor.TokenText {
			t.Errorf("4 after the expression: kind %v, want text", kind)
		}
	})
	t.Run("braces inside a Svelte expression", func(t *testing.T) {
		lines := []string{"<p>{format({ digits: 3 }) + 2} 4</p>"}
		if kind := kindAt(t, svelte, lines, 0, "2"); kind != editor.TokenNumber {
			t.Errorf("2 after the inner braces: kind %v, want number", kind)
		}
	})
	t.Run("a single closing brace ends no Vue expression", func(t *testing.T) {
		lines := []string{"<p>{{ 1 } 2</p>"}
		if kind := kindAt(t, vue, lines, 0, "1"); kind != editor.TokenText {
			t.Errorf("1: kind %v, want text", kind)
		}
	})
	t.Run("a closing brace in a string", func(t *testing.T) {
		lines := []string{`<p>{"}" + 1} 2</p>`}
		if kind := kindAt(t, svelte, lines, 0, "1"); kind != editor.TokenNumber {
			t.Errorf("1: kind %v, want number", kind)
		}
		if kind := kindAt(t, svelte, lines, 0, "2"); kind != editor.TokenText {
			t.Errorf("2 after the expression: kind %v, want text", kind)
		}
	})
	t.Run("Svelte blocks", func(t *testing.T) {
		lines := []string{"{#if ready}", "{:else}", "{/if}"}
		for i, word := range []string{"#if", ":else", "/if"} {
			if kind := kindAt(t, svelte, lines, i, word); kind != editor.TokenKeyword {
				t.Errorf("%s: kind %v, want keyword", word, kind)
			}
		}
	})
	t.Run("a comment in braces is no block", func(t *testing.T) {
		lines := []string{"<p>{/* note */}</p>"}
		if kind := kindAt(t, languageFor("a.astro"), lines, 0, "note"); kind != editor.TokenComment {
			t.Errorf("note: kind %v, want comment", kind)
		}
	})
	t.Run("an expression over two lines stays text", func(t *testing.T) {
		lines := []string{"<p>{count +", "1}</p>"}
		if kind := kindAt(t, svelte, lines, 1, "1"); kind != editor.TokenText {
			t.Errorf("1: kind %v, want text", kind)
		}
	})
	t.Run("braces in an HTML comment", func(t *testing.T) {
		lines := []string{"<!-- {1} -->"}
		if kind := kindAt(t, svelte, lines, 0, "1"); kind != editor.TokenComment {
			t.Errorf("1: kind %v, want comment", kind)
		}
	})
}
