package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestOpen(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("sub/dir/file.txt", "x\n")
	want, err := filepath.EvalSymlinks(tr.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{tr.dir, filepath.Join(tr.dir, "sub", "dir"), filepath.Join(tr.dir, "sub", "dir", "file.txt")} {
		r, err := Open(path)
		if err != nil {
			t.Fatalf("Open(%q): %v", path, err)
		}
		if r.Root != want {
			t.Errorf("Open(%q).Root = %q, want %q", path, r.Root, want)
		}
		if r.Name() != filepath.Base(want) {
			t.Errorf("Name() = %q", r.Name())
		}
	}
}

func TestOpenNotRepo(t *testing.T) {
	if _, err := Open(t.TempDir()); !errors.Is(err, ErrNotRepo) {
		t.Errorf("Open(non-repo) error = %v, want ErrNotRepo", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("Open(missing dir) succeeded")
	}
}

func TestUnbornRepo(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t).open()
	if r.HasHead(ctx) {
		t.Error("HasHead() = true in a repo without commits")
	}
	branch, err := r.CurrentBranch(ctx)
	if err != nil || branch != "main" {
		t.Errorf("CurrentBranch() = %q, %v", branch, err)
	}
	commits, err := r.Commits(ctx, "", "", 0)
	if err != nil || commits == nil || len(commits) != 0 {
		t.Errorf("Commits() = %#v, %v, want empty slice", commits, err)
	}
	if base := r.DefaultBase(ctx); base != "" {
		t.Errorf("DefaultBase() = %q, want empty", base)
	}
}

func TestBranchesAndDefaultBase(t *testing.T) {
	ctx := context.Background()
	remote := newTestRepo(t)
	remote.write("a.txt", "a\n")
	remote.commit("init")
	remote.git("branch", "release")

	tr := &testRepo{t: t, dir: filepath.Join(t.TempDir(), "clone")}
	if err := os.MkdirAll(tr.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tr.git("clone", "-q", remote.dir, ".")
	tr.git("checkout", "-q", "-b", "feature")
	r := tr.open()

	branches, err := r.Branches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"feature", "main", "origin/main", "origin/release"}
	if !slices.Equal(branches, want) {
		t.Errorf("Branches() = %q, want %q", branches, want)
	}
	if base := r.DefaultBase(ctx); base != "origin/main" {
		t.Errorf("DefaultBase() = %q, want origin/main", base)
	}
	if branch, _ := r.CurrentBranch(ctx); branch != "feature" {
		t.Errorf("CurrentBranch() = %q, want feature", branch)
	}

	tr.git("checkout", "-q", "--detach")
	if branch, err := r.CurrentBranch(ctx); err != nil || branch != "" {
		t.Errorf("CurrentBranch() detached = %q, %v", branch, err)
	}
}

func TestDefaultBaseLocal(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		{"main", "main"},
		{"master", "master"},
		{"trunk", ""},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			tr := &testRepo{t: t, dir: t.TempDir()}
			tr.git("init", "-q", "-b", tt.branch)
			tr.commit("init")
			if got := tr.open().DefaultBase(context.Background()); got != tt.want {
				t.Errorf("DefaultBase() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommits(t *testing.T) {
	ctx := context.Background()
	tr := newTestRepo(t)
	tr.write("a.txt", "1\n")
	first := tr.commit("first")
	tr.write("a.txt", "2\n")
	second := tr.commit("second\n\nsecond body\nline two")
	tr.git("checkout", "-q", "-b", "feature")
	tr.write("b.txt", "b\n")
	third := tr.commit("third")
	tr.write("b.txt", "bb\n")
	fourth := tr.commit("fourth")
	r := tr.open()

	tests := []struct {
		name  string
		base  string
		limit int
		want  []string
	}{
		{"all", "", 0, []string{fourth, third, second, first}},
		{"limited", "", 2, []string{fourth, third}},
		{"since base", "main", 0, []string{fourth, third}},
		{"since base limited", "main", 1, []string{fourth}},
		{"base is head", "feature", 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commits, err := r.Commits(ctx, tt.base, "", tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, c := range commits {
				got = append(got, c.SHA)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Commits(%q, %d) = %q, want %q", tt.base, tt.limit, got, tt.want)
			}
		})
	}

	commits, err := r.Commits(ctx, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	c := commits[2]
	if c.Subject != "second" || c.Body != "second body\nline two" {
		t.Errorf("subject %q, body %q", c.Subject, c.Body)
	}
	if c.Author != "Test Author" || c.Short != second[:len(c.Short)] || len(c.Short) < 7 {
		t.Errorf("author %q, short %q", c.Author, c.Short)
	}
	if !c.Time.Equal(time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)) {
		t.Errorf("time %v", c.Time)
	}
	if _, err := r.Commits(ctx, "no-such-branch", "", 0); err == nil {
		t.Error("Commits with unknown base succeeded")
	}
	if _, err := r.Commits(ctx, "--all", "", 0); err == nil {
		t.Error("Commits with option-like base succeeded")
	}
}

func TestGitDir(t *testing.T) {
	got, err := newTestRepo(t).open().GitDir(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != ".git" {
		t.Errorf("GitDir = %q", got)
	}
}

func TestOtherBranch(t *testing.T) {
	ctx := context.Background()
	tr := newTestRepo(t)
	tr.write("a.txt", "1\n")
	tr.commit("first")
	tr.git("checkout", "-q", "-b", "feature")
	tr.write("b.txt", "feature\n")
	third := tr.commit("feature work")
	tr.git("checkout", "-q", "main")
	tr.write("a.txt", "dirty\n")
	r := tr.open()

	commits, err := r.Commits(ctx, "main", "feature", 0)
	if err != nil || len(commits) != 1 || commits[0].SHA != third {
		t.Fatalf("Commits(main, feature) = %+v, %v", commits, err)
	}
	if _, err := r.Commits(ctx, "", "no-such-branch", 0); err == nil {
		t.Error("Commits with unknown head succeeded")
	}

	s := Scope{Kind: ScopeBranch, Base: "main", Head: "feature"}
	changes, err := r.Changes(ctx, s)
	if err != nil || len(changes) != 1 || changes[0].Path != "b.txt" {
		t.Fatalf("Changes = %+v, %v", changes, err)
	}
	_, newText, err := r.Contents(ctx, s, changes[0])
	if err != nil || newText != "feature\n" {
		t.Errorf("Contents = %q, %v", newText, err)
	}
	if s.Key() == (Scope{Kind: ScopeBranch, Base: "main"}).Key() {
		t.Error("a branch scope with a head shares its key with the current branch's")
	}
	if s.Label() != "feature vs main" || !strings.Contains(s.Describe(), "git show feature:<path>") {
		t.Errorf("label %q, describe %q", s.Label(), s.Describe())
	}
}

func TestWorktrees(t *testing.T) {
	ctx := context.Background()
	tr := newTestRepo(t)
	tr.write("a.txt", "1\n")
	tr.commit("first")
	other := filepath.Join(t.TempDir(), "wt")
	tr.git("worktree", "add", "-q", "-b", "agent", other)
	trees, err := tr.open().Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 2 || trees[0].Branch != "main" || trees[1].Branch != "agent" {
		t.Fatalf("worktrees = %+v", trees)
	}
	if got, _ := filepath.EvalSymlinks(trees[1].Path); got != mustEval(t, other) {
		t.Errorf("path = %q, want %q", trees[1].Path, other)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPatchIDFollowsCommit(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a.txt", "one\ntwo\n")
	tr.commit("init")
	r := tr.open()
	ctx := context.Background()
	uncommitted := Scope{Kind: ScopeUncommitted}
	if id, err := r.PatchID(ctx, uncommitted); err != nil || id != "" {
		t.Fatalf("clean tree: id %q, err %v", id, err)
	}
	tr.write("a.txt", "one\nTWO\nthree\n")
	tr.write("dir/new.txt", "new\n")
	before, err := r.PatchID(ctx, uncommitted)
	if err != nil || before == "" {
		t.Fatalf("uncommitted: id %q, err %v", before, err)
	}
	sha := tr.commit("change")
	after, err := r.PatchID(ctx, Scope{Kind: ScopeCommit, Commit: sha})
	if err != nil || after != before {
		t.Errorf("commit id %q, uncommitted id %q, err %v", after, before, err)
	}
	ids, err := r.CommitPatchIDs(ctx, "", "", 10)
	if err != nil || ids[before] != sha {
		t.Errorf("commit ids = %v, err %v", ids, err)
	}
}
