package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type reader struct {
	name string
	read func(FileChange) (old, new string, err error)
	file func(path string) (string, error)
}

// readers gives the three ways to read the files of s: Snapshot.Contents,
// a ContentReader, and a ContentReader whose git process cannot start.
// All must give the same result.
func readers(t *testing.T, repo *Repo, scope Scope) []reader {
	t.Helper()
	snap := snapshot(t, repo, scope)
	batch := contentReader(t, snap)
	fallback := brokenReader(t, snap)
	ctx := context.Background()
	return []reader{
		{
			"snapshot",
			func(f FileChange) (string, string, error) { return snap.Contents(ctx, f) },
			func(path string) (string, error) { return snap.File(ctx, path) },
		},
		{"reader", batch.Contents, batch.File},
		{"fallback", fallback.Contents, fallback.File},
	}
}

func contentReader(t *testing.T, snap *Snapshot) *ContentReader {
	t.Helper()
	cr := snap.NewContentReader(context.Background())
	t.Cleanup(func() {
		if err := cr.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return cr
}

// brokenReader is a ContentReader that starts a git cat-file which exits
// at once, as one older than 2.36 does.
func brokenReader(t *testing.T, snap *Snapshot) *ContentReader {
	t.Helper()
	args := catFileArgs
	catFileArgs = []string{"cat-file", "--no-such-option"}
	defer func() { catFileArgs = args }()
	return contentReader(t, snap)
}

func TestContentReaderFallsBackWhenBatchReaderExits(t *testing.T) {
	repo := uncommittedFixture(t).open()
	cr := brokenReader(t, snapshot(t, repo, Scope{Kind: ScopeUncommitted}))
	if cr.cmd != nil {
		t.Fatal("the reader kept a git process that cannot read")
	}
	old, new, err := cr.Contents(FileChange{Path: "mod.txt", Status: StatusModified})
	if err != nil || old != "a\nb\n" || new != "a\nB\nc\n" {
		t.Errorf("Contents(mod.txt) = %q, %q, %v", old, new, err)
	}
}

func TestFileOutsideChanges(t *testing.T) {
	history := newHistoryFixture(t)
	history.write("readme.md", "edited\n")
	repo := history.open()
	tests := []struct {
		name  string
		scope Scope
		path  string
		want  string
	}{
		{"uncommitted reads the working tree", Scope{Kind: ScopeUncommitted}, "src/app.go", "package app\n\nfunc A() {}\n\nfunc B() {}\n"},
		{"commit reads its own tree", Scope{Kind: ScopeCommit, Commit: history.featureB}, "src/app.go", "package app\n\nfunc A() {}\n\nfunc B() {}\n"},
		{"range reads its end", Scope{Kind: ScopeRange, From: history.second, To: history.featureA}, "rename me.txt", ""},
		{"range reads an older end", Scope{Kind: ScopeRange, From: history.root, To: history.second}, "readme.md", "hello\n"},
	}
	for _, tt := range tests {
		for _, rd := range readers(t, repo, tt.scope) {
			t.Run(rd.name+"/"+tt.name, func(t *testing.T) {
				got, err := rd.file(tt.path)
				if tt.want == "" {
					if err == nil {
						t.Errorf("File(%s) = %q, want an error", tt.path, got)
					}
					return
				}
				if err != nil || got != tt.want {
					t.Errorf("File(%s) = %q, %v, want %q", tt.path, got, err, tt.want)
				}
			})
		}
	}
	for _, rd := range readers(t, repo, Scope{Kind: ScopeUncommitted}) {
		if got, err := rd.file("../outside.txt"); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%s: File(../outside.txt) = %q, %v, want ErrInvalidPath", rd.name, got, err)
		}
	}
}

func TestContentsUncommitted(t *testing.T) {
	repo := uncommittedFixture(t).open()
	scope := Scope{Kind: ScopeUncommitted}
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
	for _, rd := range readers(t, repo, scope) {
		for _, tt := range tests {
			t.Run(rd.name+"/"+tt.name, func(t *testing.T) {
				old, new, err := rd.read(tt.file)
				if err != nil {
					t.Fatal(err)
				}
				if old != tt.old || new != tt.new {
					t.Errorf("Contents() = %q, %q, want %q, %q", old, new, tt.old, tt.new)
				}
			})
		}
	}
}

func TestContentsCommitted(t *testing.T) {
	history := newHistoryFixture(t)
	repo := history.open()
	tests := []struct {
		name     string
		scope    Scope
		file     FileChange
		old, new string
	}{
		{
			"root commit", Scope{Kind: ScopeCommit, Commit: history.root},
			FileChange{Path: "readme.md", Status: StatusAdded}, "", "hello\n",
		},
		{
			"commit rename", Scope{Kind: ScopeCommit, Commit: history.featureA},
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
			"branch added", Scope{Kind: ScopeBranch, Base: "main"},
			FileChange{Path: "feature.txt", Status: StatusAdded}, "", "feature\n",
		},
		{
			"range modified", Scope{Kind: ScopeRange, From: history.root, To: history.second},
			FileChange{Path: "src/app.go", Status: StatusModified},
			"package app\n", "package app\n\nfunc A() {}\n",
		},
	}
	for _, tt := range tests {
		for _, rd := range readers(t, repo, tt.scope) {
			t.Run(rd.name+"/"+tt.name, func(t *testing.T) {
				old, new, err := rd.read(tt.file)
				if err != nil {
					t.Fatal(err)
				}
				if old != tt.old || new != tt.new {
					t.Errorf("Contents() = %q, %q, want %q, %q", old, new, tt.old, tt.new)
				}
			})
		}
	}
}

func TestContentsErrors(t *testing.T) {
	tr := uncommittedFixture(t)
	tr.write("big.txt", strings.Repeat("x", maxContentSize+1))
	tr.write("late-nul.txt", "text\x00")
	repo := tr.open()
	scope := Scope{Kind: ScopeUncommitted}
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
	for _, rd := range readers(t, repo, scope) {
		for _, tt := range tests {
			t.Run(rd.name+"/"+tt.name, func(t *testing.T) {
				_, _, err := rd.read(tt.file)
				if !errors.Is(err, tt.want) {
					t.Errorf("Contents() error = %v, want %v", err, tt.want)
				}
			})
		}
		t.Run(rd.name+"/missing", func(t *testing.T) {
			if _, _, err := rd.read(FileChange{Path: "missing file.txt", Status: StatusModified}); err == nil {
				t.Error("Contents() of a file missing from HEAD succeeded")
			}
			// The reader keeps working after a missing file.
			if old, _, err := rd.read(FileChange{Path: "mod.txt", Status: StatusModified}); old != "a\nb\n" || err != nil {
				t.Errorf("Contents(mod.txt) = %q, %v", old, err)
			}
		})
	}
}

func TestContentsLargeBlob(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("big.txt", strings.Repeat("y", maxContentSize+1))
	tr.write("max.txt", strings.Repeat("z", maxContentSize))
	sha := tr.commit("big")
	for _, rd := range readers(t, tr.open(), Scope{Kind: ScopeCommit, Commit: sha}) {
		if _, _, err := rd.read(FileChange{Path: "big.txt", Status: StatusAdded}); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: Contents(big.txt) error = %v, want ErrTooLarge", rd.name, err)
		}
		if _, new, err := rd.read(FileChange{Path: "max.txt", Status: StatusAdded}); len(new) != maxContentSize || err != nil {
			t.Errorf("%s: Contents(max.txt) = %d bytes, %v", rd.name, len(new), err)
		}
	}
}

func TestContentsUnusualPaths(t *testing.T) {
	tr := newTestRepo(t)
	paths := []string{"ünï cödé.txt", " lead", "tab\there", "back\\slash \"quote\"", "new\nline", "cr\r", "trail "}
	for _, p := range paths {
		tr.write(p, "old\n")
	}
	tr.commit("old")
	for _, p := range paths {
		tr.write(p, "new\n")
	}
	sha := tr.commit("new")
	for _, rd := range readers(t, tr.open(), Scope{Kind: ScopeCommit, Commit: sha}) {
		for _, p := range paths {
			old, new, err := rd.read(FileChange{Path: p, Status: StatusModified})
			if old != "old\n" || new != "new\n" || err != nil {
				t.Errorf("%s: Contents(%q) = %q, %q, %v", rd.name, p, old, new, err)
			}
		}
	}
}

func TestContentsNULPath(t *testing.T) {
	repo := uncommittedFixture(t).open()
	for _, rd := range readers(t, repo, Scope{Kind: ScopeUncommitted}) {
		if _, _, err := rd.read(FileChange{Path: "mod.txt\x00info HEAD", Status: StatusModified}); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%s: Contents() error = %v, want ErrInvalidPath", rd.name, err)
		}
	}
}

func TestContentsSubmodule(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a.txt", "a\n")
	root := tr.commit("root")
	const absent = "1111111111111111111111111111111111111111"
	tr.git("update-index", "--add", "--cacheinfo", "160000,"+root+",known")
	tr.git("update-index", "--add", "--cacheinfo", "160000,"+absent+",absent")
	tr.git("commit", "-q", "-m", "submodules")
	for _, rd := range readers(t, tr.open(), Scope{Kind: ScopeCommit, Commit: "HEAD"}) {
		for path, oid := range map[string]string{"known": root, "absent": absent} {
			_, new, err := rd.read(FileChange{Path: path, Status: StatusAdded})
			if want := "Subproject commit " + oid + "\n"; new != want || err != nil {
				t.Errorf("%s: Contents(%s) = %q, %v, want %q", rd.name, path, new, err, want)
			}
		}
	}
}

func TestContentsUnbornHead(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("staged.txt", "one\ntwo\n")
	tr.git("add", "staged.txt")
	for _, rd := range readers(t, tr.open(), Scope{Kind: ScopeUncommitted}) {
		if old, new, err := rd.read(FileChange{Path: "staged.txt", Status: StatusAdded}); old != "" || new != "one\ntwo\n" || err != nil {
			t.Errorf("%s: Contents(staged.txt) = %q, %q, %v", rd.name, old, new, err)
		}
	}
}

func TestContentReaderCancelled(t *testing.T) {
	repo := uncommittedFixture(t).open()
	ctx, cancel := context.WithCancel(t.Context())
	cr := snapshot(t, repo, Scope{Kind: ScopeUncommitted}).NewContentReader(ctx)
	cancel()
	if _, _, err := cr.Contents(FileChange{Path: "mod.txt", Status: StatusModified}); !errors.Is(err, context.Canceled) {
		t.Errorf("Contents() error = %v, want context.Canceled", err)
	}
	if err := cr.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if cr.cmd.ProcessState == nil {
		t.Error("git is still running after Close")
	}
}

func TestContentReaderCancelledMidRead(t *testing.T) {
	repo := uncommittedFixture(t).open()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	cr := snapshot(t, repo, Scope{Kind: ScopeUncommitted}).NewContentReader(ctx)
	pid := cr.cmd.Process.Pid
	t.Cleanup(func() {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Error(err)
		}
	})
	// A stopped git reads the request and never answers, so Contents
	// waits in the read until the context ends.
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := cr.Contents(FileChange{Path: "mod.txt", Status: StatusModified})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Contents() error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Contents() still waits after the context ended")
	}
	if err := cr.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if cr.cmd.ProcessState == nil {
		t.Error("git is still running after Close")
	}
}

func TestContentsSymlink(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("target.txt", "t\n")
	tr.commit("init")
	if err := os.Symlink("target.txt", filepath.Join(tr.dir, "link")); err != nil {
		t.Fatal(err)
	}
	repo := tr.open()
	changes := changesByPath(t, repo, Scope{Kind: ScopeUncommitted})
	if linkChange := changes["link"]; !linkChange.Untracked || linkChange.Additions != 1 {
		t.Errorf("symlink change = %+v", linkChange)
	}
	for _, rd := range readers(t, repo, Scope{Kind: ScopeUncommitted}) {
		_, new, err := rd.read(changes["link"])
		if err != nil || new != "target.txt" {
			t.Errorf("%s: Contents(link) = %q, %v", rd.name, new, err)
		}
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
	snap := snapshot(t, tr.open(), Scope{Kind: ScopeUncommitted})
	if text, err := snap.File(t.Context(), "docs/secret.txt"); err == nil {
		t.Errorf("File(docs/secret.txt) = %q, want an error", text)
	}
}
