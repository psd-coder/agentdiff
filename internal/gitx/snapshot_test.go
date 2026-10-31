package gitx

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestSnapshotKeepsHeadAfterCommit(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a.txt", "one\n")
	tr.commit("init")
	tr.write("a.txt", "one\ntwo\n")
	r := tr.open()
	ctx := t.Context()
	snap := snapshot(t, r, Scope{Kind: ScopeUncommitted})
	sha := tr.commit("change")

	changes, err := snap.Changes(ctx)
	want := []FileChange{{Path: "a.txt", Status: StatusModified, Additions: 1}}
	if err != nil || !slices.Equal(changes, want) {
		t.Errorf("Changes() = %+v, %v, want %+v", changes, err, want)
	}
	stat, err := snap.DiffStat(ctx)
	if err != nil || !strings.Contains(stat, "a.txt | 1 +") {
		t.Errorf("DiffStat() = %q, %v", stat, err)
	}
	got, err := snap.Fingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := snapshot(t, r, Scope{Kind: ScopeCommit, Commit: sha}).Fingerprint(ctx)
	if err != nil || got.Patch == "" || got.Patch != committed.Patch || !maps.Equal(got.Files, committed.Files) {
		t.Errorf("Fingerprint() = %+v, the commit's %+v, err %v", got, committed, err)
	}
}

func TestSnapshotFixesUntrackedList(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a.txt", "one\n")
	tr.commit("init")
	tr.write("listed.txt", "x\n")
	r := tr.open()
	ctx := t.Context()
	snap := snapshot(t, r, Scope{Kind: ScopeUncommitted})
	tr.write("later.txt", "y\n")

	changes, err := snap.Changes(ctx)
	want := []FileChange{{Path: "listed.txt", Status: StatusAdded, Untracked: true, Additions: 1}}
	if err != nil || !slices.Equal(changes, want) {
		t.Errorf("Changes() = %+v, %v, want %+v", changes, err, want)
	}
	stat, err := snap.DiffStat(ctx)
	if err != nil || !strings.Contains(stat, "listed.txt") || strings.Contains(stat, "later.txt") {
		t.Errorf("DiffStat() = %q, %v", stat, err)
	}
	fp, err := snap.Fingerprint(ctx)
	if _, ok := fp.Files["later.txt"]; err != nil || ok || fp.Files["listed.txt"] == "" {
		t.Errorf("Fingerprint() files = %v, %v", fp.Files, err)
	}
}

func TestSnapshotSkipsDeletedUntrackedFile(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a.txt", "one\n")
	tr.commit("init")
	tr.write("gone.txt", "x\n")
	tr.write("kept.txt", "y\n")
	r := tr.open()
	ctx := t.Context()
	snap := snapshot(t, r, Scope{Kind: ScopeUncommitted})
	tr.remove("gone.txt")

	changes, err := snap.Changes(ctx)
	want := []FileChange{{Path: "kept.txt", Status: StatusAdded, Untracked: true, Additions: 1}}
	if err != nil || !slices.Equal(changes, want) {
		t.Errorf("Changes() = %+v, %v, want %+v", changes, err, want)
	}
	if stat, err := snap.DiffStat(ctx); err != nil || strings.Contains(stat, "gone.txt") {
		t.Errorf("DiffStat() = %q, %v", stat, err)
	}
	fp, err := snap.Fingerprint(ctx)
	if _, ok := fp.Files["gone.txt"]; err != nil || ok || fp.Files["kept.txt"] == "" {
		t.Errorf("Fingerprint() files = %v, %v", fp.Files, err)
	}
}

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
