package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
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
	os.Exit(m.Run())
}

type testRepo struct {
	t       testing.TB
	dir     string
	commits int
}

func newTestRepo(t testing.TB) *testRepo {
	t.Helper()
	tr := &testRepo{t: t, dir: t.TempDir()}
	tr.git("init", "-q", "-b", "main")
	return tr
}

func (tr *testRepo) git(args ...string) string {
	tr.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", tr.dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tr.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (tr *testRepo) write(path, content string) {
	tr.t.Helper()
	full := filepath.Join(tr.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr *testRepo) remove(path string) {
	tr.t.Helper()
	if err := os.Remove(filepath.Join(tr.dir, path)); err != nil {
		tr.t.Fatal(err)
	}
}

// commit stages everything and commits with an increasing date so log order is stable.
func (tr *testRepo) commit(msg string) string {
	tr.t.Helper()
	tr.commits++
	date := fmt.Sprintf("2026-01-01T00:00:%02dZ", tr.commits)
	tr.git("add", "-A")
	cmd := exec.Command("git", "-C", tr.dir, "commit", "-q", "--allow-empty", "-m", msg)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		tr.t.Fatalf("git commit: %v\n%s", err, out)
	}
	return tr.git("rev-parse", "HEAD")
}

func (tr *testRepo) open() *Repo {
	tr.t.Helper()
	r, err := Open(tr.dir)
	if err != nil {
		tr.t.Fatal(err)
	}
	return r
}

func changesByPath(t *testing.T, r *Repo, s Scope) map[string]FileChange {
	t.Helper()
	changes, err := r.Changes(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[string]FileChange, len(changes))
	for _, c := range changes {
		m[c.Path] = c
	}
	return m
}
