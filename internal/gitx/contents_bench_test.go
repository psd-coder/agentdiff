package gitx

import (
	"fmt"
	"testing"
)

const benchFiles = 200

// benchScopes builds a repo where every scope changes the same 200 files:
// a branch commit on top of main, and an edit in the working tree on top.
func benchScopes(b *testing.B) (*Repo, map[string]Scope) {
	tr := newTestRepo(b)
	for i := range benchFiles {
		tr.write(fmt.Sprintf("pkg/f%03d.go", i), "package pkg\n\n// v1\n")
	}
	tr.commit("base")
	tr.git("checkout", "-q", "-b", "feature")
	for i := range benchFiles {
		tr.write(fmt.Sprintf("pkg/f%03d.go", i), "package pkg\n\n// v2\n")
	}
	tr.commit("change")
	for i := range benchFiles {
		tr.write(fmt.Sprintf("pkg/f%03d.go", i), "package pkg\n\n// v3\n")
	}
	return tr.open(), map[string]Scope{
		"uncommitted": {Kind: ScopeUncommitted},
		"branch":      {Kind: ScopeBranch, Base: "main"},
		"commit":      {Kind: ScopeCommit, Commit: "HEAD"},
	}
}

func BenchmarkLoad(b *testing.B) {
	r, scopes := benchScopes(b)
	for _, name := range []string{"uncommitted", "branch", "commit"} {
		s := scopes[name]
		files, err := r.Changes(b.Context(), s)
		if err != nil || len(files) != benchFiles {
			b.Fatalf("Changes(%s) = %d files, %v", name, len(files), err)
		}
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				cr, err := r.NewContentReader(b.Context(), s)
				if err != nil {
					b.Fatal(err)
				}
				for _, f := range files {
					if _, _, err := cr.Contents(f); err != nil {
						b.Fatal(err)
					}
				}
				if err := cr.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFile(b *testing.B) {
	r, scopes := benchScopes(b)
	for _, name := range []string{"uncommitted", "branch", "commit"} {
		s := scopes[name]
		files, err := r.Changes(b.Context(), s)
		if err != nil || len(files) != benchFiles {
			b.Fatalf("Changes(%s) = %d files, %v", name, len(files), err)
		}
		b.Run(name, func(b *testing.B) {
			cr, err := r.NewContentReader(b.Context(), s)
			if err != nil {
				b.Fatal(err)
			}
			defer cr.Close()
			i := 0
			for b.Loop() {
				if _, _, err := cr.Contents(files[i%len(files)]); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})
	}
}
