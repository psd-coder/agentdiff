package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentsUncommitted(t *testing.T) {
	r := uncommittedFixture(t).open()
	s := Scope{Kind: ScopeUncommitted}
	tests := []struct {
		name     string
		file     FileChange
		old, new string
	}{
		{"modified", FileChange{Path: "mod.txt", Status: StatusModified}, "a\nb\n", "a\nB\nc\n"},
		{"deleted", FileChange{Path: "del.txt", Status: StatusDeleted}, "gone\n", ""},
		{"renamed", FileChange{Path: "new name.txt", OldPath: "old name.txt", Status: StatusRenamed}, "same content\nacross\nthe rename\n", "same content\nacross\nthe rename\n"},
		{"staged", FileChange{Path: "staged.txt", Status: StatusAdded}, "", "staged\n"},
		{"untracked", FileChange{Path: "dir/untracked file.txt", Status: StatusAdded, Untracked: true}, "", "x\ny"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old, new, err := r.Contents(t.Context(), s, tt.file)
			if err != nil {
				t.Fatal(err)
			}
			if old != tt.old || new != tt.new {
				t.Errorf("Contents() = %q, %q, want %q, %q", old, new, tt.old, tt.new)
			}
		})
	}
}

func TestContentsCommitted(t *testing.T) {
	h := newHistoryFixture(t)
	r := h.open()
	tests := []struct {
		name     string
		scope    Scope
		file     FileChange
		old, new string
	}{
		{
			"root commit", Scope{Kind: ScopeCommit, Commit: h.root},
			FileChange{Path: "readme.md", Status: StatusAdded}, "", "hello\n",
		},
		{
			"commit rename", Scope{Kind: ScopeCommit, Commit: h.featureA},
			FileChange{Path: "renamed.txt", OldPath: "rename me.txt", Status: StatusRenamed},
			"line one\nline two\nline three\n", "line one\nline two\nline three\n",
		},
		{
			"branch modified uses merge base", Scope{Kind: ScopeBranch, Base: "main"},
			FileChange{Path: "src/app.go", Status: StatusModified},
			"package app\n\nfunc A() {}\n", "package app\n\nfunc A() {}\n\nfunc B() {}\n",
		},
		{
			"branch deleted", Scope{Kind: ScopeBranch, Base: "main"},
			FileChange{Path: "readme.md", Status: StatusDeleted}, "hello\n", "",
		},
		{
			"range modified", Scope{Kind: ScopeRange, From: h.root, To: h.second},
			FileChange{Path: "src/app.go", Status: StatusModified},
			"package app\n", "package app\n\nfunc A() {}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old, new, err := r.Contents(t.Context(), tt.scope, tt.file)
			if err != nil {
				t.Fatal(err)
			}
			if old != tt.old || new != tt.new {
				t.Errorf("Contents() = %q, %q, want %q, %q", old, new, tt.old, tt.new)
			}
		})
	}
}

func TestContentsErrors(t *testing.T) {
	tr := uncommittedFixture(t)
	tr.write("big.txt", strings.Repeat("x", maxContentSize+1))
	tr.write("late-nul.txt", "text\x00")
	r := tr.open()
	s := Scope{Kind: ScopeUncommitted}
	tests := []struct {
		name string
		file FileChange
		want error
	}{
		{"binary flag", FileChange{Path: "bin.dat", Status: StatusModified, Binary: true}, ErrBinary},
		{"binary content", FileChange{Path: "late-nul.txt", Status: StatusAdded, Untracked: true}, ErrBinary},
		{"binary old side", FileChange{Path: "bin.dat", Status: StatusModified}, ErrBinary},
		{"too large", FileChange{Path: "big.txt", Status: StatusAdded, Untracked: true}, ErrTooLarge},
		{"escapes root", FileChange{Path: "../outside.txt", Status: StatusAdded}, ErrInvalidPath},
		{"absolute", FileChange{Path: "/etc/hosts", Status: StatusAdded}, ErrInvalidPath},
		{"old path escapes", FileChange{Path: "a", OldPath: "../b", Status: StatusRenamed}, ErrInvalidPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := r.Contents(t.Context(), s, tt.file)
			if !errors.Is(err, tt.want) {
				t.Errorf("Contents() error = %v, want %v", err, tt.want)
			}
		})
	}
	if _, _, err := r.Contents(t.Context(), s, FileChange{Path: "missing.txt", Status: StatusModified}); err == nil {
		t.Error("Contents() of a file missing from HEAD succeeded")
	}
}

func TestContentsLargeBlob(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("big.txt", strings.Repeat("y", maxContentSize+1))
	sha := tr.commit("big")
	_, _, err := tr.open().Contents(t.Context(), Scope{Kind: ScopeCommit, Commit: sha}, FileChange{Path: "big.txt", Status: StatusAdded})
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("Contents() error = %v, want ErrTooLarge", err)
	}
}

func TestContentsSymlink(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("target.txt", "t\n")
	tr.commit("init")
	if err := os.Symlink("target.txt", filepath.Join(tr.dir, "link")); err != nil {
		t.Fatal(err)
	}
	r := tr.open()
	changes := changesByPath(t, r, Scope{Kind: ScopeUncommitted})
	if c := changes["link"]; !c.Untracked || c.Additions != 1 {
		t.Errorf("symlink change = %+v", c)
	}
	_, new, err := r.Contents(t.Context(), Scope{Kind: ScopeUncommitted}, changes["link"])
	if err != nil || new != "target.txt" {
		t.Errorf("Contents(link) = %q, %v", new, err)
	}
}

func TestContentsSymlinkedDirOutsideRepo(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := newTestRepo(t)
	if err := os.Symlink(outside, filepath.Join(tr.dir, "docs")); err != nil {
		t.Fatal(err)
	}
	tr.commit("link docs")
	file := FileChange{Path: "docs/secret.txt", Status: StatusAdded}
	if _, new, err := tr.open().Contents(t.Context(), Scope{Kind: ScopeUncommitted}, file); err == nil {
		t.Errorf("Contents(docs/secret.txt) = %q, want an error", new)
	}
}
