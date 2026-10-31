package gitx

import (
	"regexp"
	"strings"
	"testing"
)

func TestDiffStatUncommitted(t *testing.T) {
	r := uncommittedFixture(t).open()
	stat, err := snapshot(t, r, Scope{Kind: ScopeUncommitted}).DiffStat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`mod\.txt +\| +3 \+\+-\n`),
		regexp.MustCompile(`del\.txt +\| +1 -\n`),
		regexp.MustCompile(`(?m)^ dir/untracked file\.txt \| 2 \+\+ \(untracked\)$`),
		regexp.MustCompile(`(?m)^ untracked\.bin \| Bin \(untracked\)$`),
		regexp.MustCompile(`files changed`),
	} {
		if !want.MatchString(stat) {
			t.Errorf("DiffStat() missing %q:\n%s", want, stat)
		}
	}
	if strings.Contains(stat, "ignored.log") {
		t.Errorf("DiffStat() lists an ignored file:\n%s", stat)
	}
}

func TestDiffStatBranch(t *testing.T) {
	h := newHistoryFixture(t)
	stat, err := snapshot(t, h.open(), Scope{Kind: ScopeBranch, Base: "main"}).DiffStat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stat, "src/app.go") || strings.Contains(stat, "main-only.txt") {
		t.Errorf("DiffStat() =\n%s", stat)
	}
}

func TestPatchUncommitted(t *testing.T) {
	r := uncommittedFixture(t).open()
	patch, truncated, err := r.Patch(t.Context(), Scope{Kind: ScopeUncommitted}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("Patch() without a limit reports truncation")
	}
	for _, want := range []string{
		"diff --git a/mod.txt b/mod.txt",
		"-b\n+B\n+c\n",
		"rename from old name.txt",
		"deleted file mode",
		"diff --git a/dir/untracked file.txt b/dir/untracked file.txt",
		"+x\n+y\n\\ No newline at end of file",
		"Binary files /dev/null and b/untracked.bin differ",
	} {
		if !strings.Contains(patch, want) {
			t.Errorf("Patch() missing %q:\n%s", want, patch)
		}
	}
}

func TestPatchTruncates(t *testing.T) {
	r := uncommittedFixture(t).open()
	full, _, err := r.Patch(t.Context(), Scope{Kind: ScopeUncommitted}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		max       int
		truncated bool
	}{
		{"tiny", 50, true},
		{"before untracked", strings.Index(full, "diff --git a/.gitignore"), true},
		{"exact", len(full), false},
		{"larger", len(full) + 100, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, truncated, err := r.Patch(t.Context(), Scope{Kind: ScopeUncommitted}, tt.max)
			if err != nil {
				t.Fatal(err)
			}
			if truncated != tt.truncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.truncated)
			}
			if len(patch) > tt.max {
				t.Errorf("len(patch) = %d > %d", len(patch), tt.max)
			}
			if !strings.HasPrefix(full, patch) {
				t.Error("truncated patch is not a prefix of the full patch")
			}
			if truncated && patch != "" && !strings.HasSuffix(patch, "\n") {
				t.Error("truncated patch does not end at a line boundary")
			}
		})
	}
}

func TestPatchCommit(t *testing.T) {
	h := newHistoryFixture(t)
	r := h.open()
	patch, _, err := r.Patch(t.Context(), Scope{Kind: ScopeCommit, Commit: h.root}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "new file mode") || !strings.Contains(patch, "+hello") {
		t.Errorf("root commit patch =\n%s", patch)
	}
}

func TestPatchUnbornHead(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("staged.txt", "s\n")
	tr.git("add", "staged.txt")
	tr.write("new file.txt", "n\n")
	patch, _, err := tr.open().Patch(t.Context(), Scope{Kind: ScopeUncommitted}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "b/staged.txt") || !strings.Contains(patch, "b/new file.txt") {
		t.Errorf("Patch() =\n%s", patch)
	}
}
