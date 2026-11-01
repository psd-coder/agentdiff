package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(tests *testing.M) {
	env := map[string]string{
		"GIT_CONFIG_GLOBAL":   "/dev/null",
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "Test Author",
		"GIT_AUTHOR_EMAIL":    "author@example.com",
		"GIT_COMMITTER_NAME":  "Test Committer",
		"GIT_COMMITTER_EMAIL": "committer@example.com",
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	os.Exit(tests.Run())
}

type testRepo struct {
	currentTest testing.TB
	dir         string
	commits     int
}

func newTestRepo(t testing.TB) *testRepo {
	t.Helper()
	tr := &testRepo{currentTest: t, dir: t.TempDir()}
	tr.git("init", "-q", "-b", "main")
	return tr
}

func (tr *testRepo) git(args ...string) string {
	tr.currentTest.Helper()
	cmd := exec.Command("git", append([]string{"-C", tr.dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tr.currentTest.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (tr *testRepo) write(path, content string) {
	tr.currentTest.Helper()
	full := filepath.Join(tr.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.currentTest.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		tr.currentTest.Fatal(err)
	}
}

func (tr *testRepo) remove(path string) {
	tr.currentTest.Helper()
	if err := os.Remove(filepath.Join(tr.dir, path)); err != nil {
		tr.currentTest.Fatal(err)
	}
}

// commit stages everything and commits with an increasing date so log order is stable.
func (tr *testRepo) commit(msg string) string {
	tr.currentTest.Helper()
	tr.commits++
	date := fmt.Sprintf("2026-01-01T00:00:%02dZ", tr.commits)
	tr.git("add", "-A")
	cmd := exec.Command("git", "-C", tr.dir, "commit", "-q", "--allow-empty", "-m", msg)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		tr.currentTest.Fatalf("git commit: %v\n%s", err, out)
	}
	return tr.git("rev-parse", "HEAD")
}

func (tr *testRepo) open() *Repo {
	tr.currentTest.Helper()
	repo, err := Open(tr.dir)
	if err != nil {
		tr.currentTest.Fatal(err)
	}
	return repo
}

func snapshot(t testing.TB, repo *Repo, scope Scope) *Snapshot {
	t.Helper()
	snap, err := repo.Snapshot(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func changesByPath(t *testing.T, repo *Repo, scope Scope) map[string]FileChange {
	t.Helper()
	changes, err := snapshot(t, repo, scope).Changes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]FileChange, len(changes))
	for _, c := range changes {
		byPath[c.Path] = c
	}
	return byPath
}
