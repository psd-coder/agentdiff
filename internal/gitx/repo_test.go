package gitx

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
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
		repo, err := Open(path)
		if err != nil {
			t.Fatalf("Open(%q): %v", path, err)
		}
		if repo.Root != want {
			t.Errorf("Open(%q).Root = %q, want %q", path, repo.Root, want)
		}
		if repo.Name() != filepath.Base(want) {
			t.Errorf("Name() = %q", repo.Name())
		}
	}
}

func TestOpenNotRepo(t *testing.T) {
	_, err := Open(t.TempDir())
	if !errors.Is(err, ErrNotRepo) {
		t.Errorf("Open(non-repo) error = %v, want ErrNotRepo", err)
	}
	if err != nil && !strings.Contains(err.Error(), "fatal: not a git repository") {
		t.Errorf("Open(non-repo) error = %v, want git's message", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("Open(missing dir) succeeded")
	}
}

func TestOpenWithoutGit(t *testing.T) {
	tr := newTestRepo(t)
	t.Setenv("PATH", t.TempDir())
	if _, err := Open(tr.dir); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("Open without git error = %v, want exec.ErrNotFound", err)
	}
}

func TestUnbornRepo(t *testing.T) {
	ctx := t.Context()
	repo := newTestRepo(t).open()
	if repo.HasHead(ctx) {
		t.Error("HasHead() = true in a repo without commits")
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil || branch != "main" {
		t.Errorf("CurrentBranch() = %q, %v", branch, err)
	}
	log, err := repo.CommitLog(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	commits, err := log.Page(ctx, 0, 10)
	if err != nil || commits == nil || len(commits) != 0 {
		t.Errorf("Page() = %#v, %v, want empty slice", commits, err)
	}
	if count, err := log.Count(ctx); count != 0 || err != nil {
		t.Errorf("Count() = %d, %v, want 0", count, err)
	}
	if base := repo.DefaultBase(ctx); base != "" {
		t.Errorf("DefaultBase() = %q, want empty", base)
	}
}

func TestBranchesAndDefaultBase(t *testing.T) {
	ctx := t.Context()
	remote := newTestRepo(t)
	remote.write("a.txt", "a\n")
	remote.commit("init")
	remote.git("branch", "release")

	tr := &testRepo{currentTest: t, dir: filepath.Join(t.TempDir(), "clone")}
	if err := os.MkdirAll(tr.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tr.git("clone", "-q", remote.dir, ".")
	tr.git("checkout", "-q", "-b", "feature")
	repo := tr.open()

	branches, err := repo.Branches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"feature", "main", "origin/main", "origin/release"}
	if !slices.Equal(branches, want) {
		t.Errorf("Branches() = %q, want %q", branches, want)
	}
	if base := repo.DefaultBase(ctx); base != "origin/main" {
		t.Errorf("DefaultBase() = %q, want origin/main", base)
	}
	if branch, _ := repo.CurrentBranch(ctx); branch != "feature" {
		t.Errorf("CurrentBranch() = %q, want feature", branch)
	}

	tr.git("checkout", "-q", "--detach")
	if branch, err := repo.CurrentBranch(ctx); err != nil || branch != "" {
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
			tr := &testRepo{currentTest: t, dir: t.TempDir()}
			tr.git("init", "-q", "-b", tt.branch)
			tr.commit("init")
			if got := tr.open().DefaultBase(t.Context()); got != tt.want {
				t.Errorf("DefaultBase() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommits(t *testing.T) {
	ctx := t.Context()
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
	repo := tr.open()

	tests := []struct {
		name        string
		base        string
		skip, limit int
		want        []string
	}{
		{"all", "", 0, 10, []string{fourth, third, second, first}},
		{"limited", "", 0, 2, []string{fourth, third}},
		{"skipped", "", 1, 2, []string{third, second}},
		{"past the end", "", 4, 2, nil},
		{"since base", "main", 0, 10, []string{fourth, third}},
		{"since base skipped", "main", 1, 10, []string{third}},
		{"base is head", "feature", 0, 10, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, err := repo.CommitLog(ctx, tt.base, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := pageSHAs(t, log, tt.skip, tt.limit); !slices.Equal(got, tt.want) {
				t.Errorf("Page(%d, %d) since %q = %q, want %q", tt.skip, tt.limit, tt.base, got, tt.want)
			}
		})
	}

	log, err := repo.CommitLog(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	commits, err := log.Page(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	commit := commits[2]
	if commit.Subject != "second" || commit.Body != "second body\nline two" {
		t.Errorf("subject %q, body %q", commit.Subject, commit.Body)
	}
	if commit.Author != "Test Author" || commit.Short != second[:len(commit.Short)] || len(commit.Short) < 7 {
		t.Errorf("author %q, short %q", commit.Author, commit.Short)
	}
	if !commit.Time.Equal(time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)) {
		t.Errorf("time %v", commit.Time)
	}
	if _, err := repo.CommitLog(ctx, "no-such-branch", ""); err == nil {
		t.Error("CommitLog with unknown base succeeded")
	}
	if _, err := repo.CommitLog(ctx, "--all", ""); err == nil {
		t.Error("CommitLog with option-like base succeeded")
	}
}

func TestCommit(t *testing.T) {
	ctx := t.Context()
	tr := newTestRepo(t)
	tr.write("a.txt", "1\n")
	sha := tr.commit("Subject line\n\nFirst body line\nsecond body line")
	tr.write("a.txt", "2\n")
	tr.commit("later")
	repo := tr.open()
	got, err := repo.Commit(ctx, "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA != sha || got.Subject != "Subject line" || got.Body != "First body line\nsecond body line" {
		t.Errorf("Commit = %+v", got)
	}
	for _, ref := range []string{"nope", "--all"} {
		if _, err := repo.Commit(ctx, ref); err == nil {
			t.Errorf("Commit(%q) succeeded", ref)
		}
	}
}

// TestCommitLogKeepsItsCommits reads the pages of a log while the branch
// gets a new commit: they hold the commits of the branch when the log was
// made, each once.
func TestCommitLogKeepsItsCommits(t *testing.T) {
	ctx := t.Context()
	tr := newTestRepo(t)
	tr.commit("base")
	tr.git("checkout", "-q", "-b", "feature")
	var want []string
	for i := range 5 {
		want = append([]string{tr.commit(fmt.Sprint("work ", i))}, want...)
	}
	repo := tr.open()
	log, err := repo.CommitLog(ctx, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	got := pageSHAs(t, log, 0, 2)
	tr.commit("new work")
	got = append(got, pageSHAs(t, log, 2, 2)...)
	got = append(got, pageSHAs(t, log, 4, 2)...)
	if !slices.Equal(got, want) {
		t.Errorf("pages = %q, want %q", got, want)
	}
	if count, err := log.Count(ctx); count != 5 || err != nil {
		t.Errorf("Count() = %d, %v, want 5", count, err)
	}
	log, err = repo.CommitLog(ctx, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if count, err := log.Count(ctx); count != 6 || err != nil {
		t.Errorf("Count() of a new log = %d, %v, want 6", count, err)
	}
}

func pageSHAs(t *testing.T, log CommitLog, skip, limit int) []string {
	t.Helper()
	commits, err := log.Page(t.Context(), skip, limit)
	if err != nil {
		t.Fatal(err)
	}
	var shas []string
	for _, c := range commits {
		shas = append(shas, c.SHA)
	}
	return shas
}

func TestGitDir(t *testing.T) {
	got, err := newTestRepo(t).open().GitDir(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != ".git" {
		t.Errorf("GitDir = %q", got)
	}
}

func TestCommonDirOfLinkedWorktree(t *testing.T) {
	ctx := t.Context()
	tr := newTestRepo(t)
	tr.commit("first")
	other := filepath.Join(t.TempDir(), "wt")
	tr.git("worktree", "add", "-q", "-b", "agent", other)
	repo, err := Open(other)
	if err != nil {
		t.Fatal(err)
	}
	common, err := repo.CommonDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mustEval(t, tr.dir), ".git"); mustEval(t, common) != want {
		t.Errorf("CommonDir = %q, want %q", common, want)
	}
	gitDir, err := repo.GitDir(ctx)
	if err != nil || mustEval(t, gitDir) == mustEval(t, common) {
		t.Errorf("GitDir = %q, %v: same as the common dir", gitDir, err)
	}
}

func TestIgnored(t *testing.T) {
	tr := newTestRepo(t)
	tr.write(".gitignore", "dist/\n*.log\n")
	tr.write("dist/sub/out.js", "x\n")
	tr.write("src/a.go", "package a\n")
	got, err := tr.open().Ignored(t.Context(), []string{".", "dist", "dist/sub", "src", "src/debug.log"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"dist": true, "dist/sub": true, "src/debug.log": true}
	if !maps.Equal(got, want) {
		t.Errorf("Ignored = %v, want %v", got, want)
	}
	got, err = tr.open().Ignored(t.Context(), []string{"src"})
	if err != nil || len(got) != 0 {
		t.Errorf("Ignored(src) = %v, %v", got, err)
	}
}

func TestOtherBranch(t *testing.T) {
	ctx := t.Context()
	tr := newTestRepo(t)
	tr.write("a.txt", "1\n")
	tr.commit("first")
	tr.git("checkout", "-q", "-b", "feature")
	tr.write("b.txt", "feature\n")
	third := tr.commit("feature work")
	tr.git("checkout", "-q", "main")
	tr.write("a.txt", "dirty\n")
	repo := tr.open()

	log, err := repo.CommitLog(ctx, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if got := pageSHAs(t, log, 0, 10); !slices.Equal(got, []string{third}) {
		t.Fatalf("Page() of main..feature = %q", got)
	}
	if _, err := repo.CommitLog(ctx, "", "no-such-branch"); err == nil {
		t.Error("CommitLog with unknown head succeeded")
	}

	scope := Scope{Kind: ScopeBranch, Base: "main", Head: "feature"}
	snap := snapshot(t, repo, scope)
	changes, err := snap.Changes(ctx)
	if err != nil || len(changes) != 1 || changes[0].Path != "b.txt" {
		t.Fatalf("Changes = %+v, %v", changes, err)
	}
	_, newText, err := snap.Contents(ctx, changes[0])
	if err != nil || newText != "feature\n" {
		t.Errorf("Contents = %q, %v", newText, err)
	}
	if scope.Key() == (Scope{Kind: ScopeBranch, Base: "main"}).Key() {
		t.Error("a branch scope with a head shares its key with the current branch's")
	}
	if scope.Label() != "feature vs main" || !strings.Contains(scope.Describe(), "git show feature:<path>") {
		t.Errorf("label %q, describe %q", scope.Label(), scope.Describe())
	}
}

func TestWorktrees(t *testing.T) {
	ctx := t.Context()
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

func mustEval(t *testing.T, path string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFingerprintFollowsCommit(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a.txt", "one\ntwo\n")
	tr.write("old.txt", "old\n")
	tr.commit("init")
	repo := tr.open()
	ctx := t.Context()
	uncommitted := Scope{Kind: ScopeUncommitted}
	if fp, err := snapshot(t, repo, uncommitted).Fingerprint(ctx); err != nil || fp.Patch != "" || len(fp.Files) != 0 {
		t.Fatalf("clean tree: %+v, err %v", fp, err)
	}
	tr.write("a.txt", "one\nTWO\nthree\n")
	tr.write("dir/new file.txt", "new\n")
	tr.write("pic.bin", "\x00\x01")
	if err := os.Remove(filepath.Join(tr.dir, "old.txt")); err != nil {
		t.Fatal(err)
	}
	before, err := snapshot(t, repo, uncommitted).Fingerprint(ctx)
	if err != nil || before.Patch == "" {
		t.Fatalf("uncommitted: %+v, err %v", before, err)
	}
	if len(before.Files) != 4 || before.Files["a.txt"] == "" || before.Files["dir/new file.txt"] == "" || before.Files["old.txt"] == "" {
		t.Fatalf("files = %v", before.Files)
	}
	if _, ok := before.Files["pic.bin"]; !ok {
		t.Errorf("binary file missing: %v", before.Files)
	}
	sha := tr.commit("change")
	after, err := snapshot(t, repo, Scope{Kind: ScopeCommit, Commit: sha}).Fingerprint(ctx)
	if err != nil || after.Patch != before.Patch || !maps.Equal(after.Files, before.Files) {
		t.Errorf("commit %+v, uncommitted %+v, err %v", after, before, err)
	}
	ids, err := repo.CommitPatchIDs(ctx, "", "", 10)
	if err != nil || ids[before.Patch] != sha {
		t.Errorf("commit ids = %v, err %v", ids, err)
	}
	tr.write("a.txt", "again\n")
	again := tr.commit("again")
	got, err := repo.CommitsWithFiles(ctx, map[string][]string{
		"both": {"a.txt", "dir/new file.txt"}, "old": {"old.txt"}, "a": {"a.txt"}, "nope": {"a.txt", "nope"}, "empty": nil,
	}, 10)
	want := map[string]string{"both": sha, "old": sha, "a": again}
	if err != nil || !maps.Equal(got, want) {
		t.Errorf("commits with files = %v, want %v, err %v", got, want, err)
	}
}

func TestFingerprintIgnoresDiffPrefixConfig(t *testing.T) {
	for _, key := range []string{"diff.mnemonicPrefix", "diff.noprefix"} {
		t.Run(key, func(t *testing.T) {
			tr := newTestRepo(t)
			tr.write("a.txt", "one\ntwo\n")
			tr.commit("init")
			repo := tr.open()
			ctx := t.Context()
			uncommitted := Scope{Kind: ScopeUncommitted}
			tr.write("a.txt", "one\nTWO\n")
			tr.write("dir/new file.txt", "new\n")
			tr.write("pic.bin", "\x00\x01")
			want, err := snapshot(t, repo, uncommitted).Fingerprint(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tr.git("config", key, "true")
			got, err := snapshot(t, repo, uncommitted).Fingerprint(ctx)
			if err != nil || got.Patch != want.Patch || !maps.Equal(got.Files, want.Files) {
				t.Fatalf("with %s %+v, without %+v, err %v", key, got, want, err)
			}
			for _, path := range []string{"a.txt", "dir/new file.txt", "pic.bin"} {
				if _, ok := got.Files[path]; !ok {
					t.Errorf("file %q missing: %v", path, got.Files)
				}
			}
			sha := tr.commit("change")
			ids, err := repo.CommitPatchIDs(ctx, "", "", 10)
			if err != nil || ids[want.Patch] != sha {
				t.Errorf("commit ids = %v, want %s for %s, err %v", ids, sha, want.Patch, err)
			}
		})
	}
}

func TestChunkPath(t *testing.T) {
	for _, tc := range []struct{ diff, want string }{
		{"diff --git a/x.go b/x.go\nindex 1..2 100644\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n", "x.go"},
		{"diff --git a/gone.go b/gone.go\ndeleted file mode 100644\n--- a/gone.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-a\n", "gone.go"},
		{"diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n", "new.go"},
		{"diff --git a/p.png b/p.png\nindex 1..2 100644\nBinary files a/p.png and b/p.png differ\n", "p.png"},
		{"diff --git \"a/sp\\303\\251.go\" \"b/sp\\303\\251.go\"\n--- \"a/sp\\303\\251.go\"\n+++ \"b/sp\\303\\251.go\"\n@@ -1 +1 @@\n", "spé.go"},
	} {
		chunks := splitPatch([]byte(tc.diff + tc.diff))
		if len(chunks) != 2 || chunks[0].path != tc.want || string(chunks[1].text) != tc.diff {
			t.Errorf("%q: chunks %+v, want path %q", tc.diff, chunks, tc.want)
		}
	}
}
