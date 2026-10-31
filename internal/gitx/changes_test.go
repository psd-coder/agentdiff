package gitx

import (
	"slices"
	"strings"
	"testing"
)

const binaryContent = "PNG\x00\x01\x02binary\x00data"

// uncommittedFixture commits a base and leaves every kind of working tree change on top.
func uncommittedFixture(t *testing.T) *testRepo {
	tr := newTestRepo(t)
	tr.write("keep.txt", "keep\n")
	tr.write("mod.txt", "a\nb\n")
	tr.write("del.txt", "gone\n")
	tr.write("old name.txt", "same content\nacross\nthe rename\n")
	tr.write("bin.dat", binaryContent)
	tr.commit("base")

	tr.write("mod.txt", "a\nB\nc\n")
	tr.remove("del.txt")
	tr.git("mv", "old name.txt", "new name.txt")
	tr.write("staged.txt", "staged\n")
	tr.git("add", "staged.txt")
	tr.write("bin.dat", binaryContent+"\x00more")
	tr.write("dir/untracked file.txt", "x\ny")
	tr.write("untracked.bin", "\x00\x01")
	tr.write("ignored.log", "ignored\n")
	tr.write(".gitignore", "*.log\n")
	return tr
}

func TestChangesUncommitted(t *testing.T) {
	tr := uncommittedFixture(t)
	r := tr.open()
	got, err := r.Changes(t.Context(), Scope{Kind: ScopeUncommitted})
	if err != nil {
		t.Fatal(err)
	}
	want := []FileChange{
		{Path: ".gitignore", Status: StatusAdded, Untracked: true, Additions: 1},
		{Path: "bin.dat", Status: StatusModified, Binary: true},
		{Path: "del.txt", Status: StatusDeleted, Deletions: 1},
		{Path: "dir/untracked file.txt", Status: StatusAdded, Untracked: true, Additions: 2},
		{Path: "mod.txt", Status: StatusModified, Additions: 2, Deletions: 1},
		{Path: "new name.txt", OldPath: "old name.txt", Status: StatusRenamed},
		{Path: "staged.txt", Status: StatusAdded, Additions: 1},
		{Path: "untracked.bin", Status: StatusAdded, Untracked: true, Binary: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Changes() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestChangesUnbornHead(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("staged.txt", "one\ntwo\n")
	tr.git("add", "staged.txt")
	tr.write("untracked one.txt", "u\n")
	got, err := tr.open().Changes(t.Context(), Scope{Kind: ScopeUncommitted})
	if err != nil {
		t.Fatal(err)
	}
	want := []FileChange{
		{Path: "staged.txt", Status: StatusAdded, Additions: 2},
		{Path: "untracked one.txt", Status: StatusAdded, Untracked: true, Additions: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Changes() = %+v, want %+v", got, want)
	}
}

func TestChangesCleanTree(t *testing.T) {
	tr := newTestRepo(t)
	tr.write("a.txt", "a\n")
	tr.commit("init")
	got, err := tr.open().Changes(t.Context(), Scope{Kind: ScopeUncommitted})
	if err != nil || len(got) != 0 {
		t.Errorf("Changes() = %+v, %v, want none", got, err)
	}
}

// historyFixture builds main with a root commit and a second commit, and a feature
// branch with two more commits, while main also moves on after the fork.
type historyFixture struct {
	*testRepo
	root, second, featureA, featureB, mainAfter string
}

func newHistoryFixture(t *testing.T) historyFixture {
	tr := newTestRepo(t)
	h := historyFixture{testRepo: tr}
	tr.write("readme.md", "hello\n")
	tr.write("src/app.go", "package app\n")
	h.root = tr.commit("root")
	tr.write("src/app.go", "package app\n\nfunc A() {}\n")
	tr.write("rename me.txt", "line one\nline two\nline three\n")
	h.second = tr.commit("second")

	tr.git("checkout", "-q", "-b", "feature")
	tr.write("src/app.go", "package app\n\nfunc A() {}\n\nfunc B() {}\n")
	tr.git("mv", "rename me.txt", "renamed.txt")
	h.featureA = tr.commit("feature a")
	tr.write("feature.txt", "feature\n")
	tr.remove("readme.md")
	h.featureB = tr.commit("feature b")

	tr.git("checkout", "-q", "main")
	tr.write("main-only.txt", "main\n")
	h.mainAfter = tr.commit("main after fork")
	tr.git("checkout", "-q", "feature")
	return h
}

func TestChangesCommittedScopes(t *testing.T) {
	h := newHistoryFixture(t)
	r := h.open()
	tests := []struct {
		name  string
		scope Scope
		want  []FileChange
	}{
		{
			name:  "root commit",
			scope: Scope{Kind: ScopeCommit, Commit: h.root},
			want: []FileChange{
				{Path: "readme.md", Status: StatusAdded, Additions: 1},
				{Path: "src/app.go", Status: StatusAdded, Additions: 1},
			},
		},
		{
			name:  "commit with rename",
			scope: Scope{Kind: ScopeCommit, Commit: h.featureA},
			want: []FileChange{
				{Path: "renamed.txt", OldPath: "rename me.txt", Status: StatusRenamed},
				{Path: "src/app.go", Status: StatusModified, Additions: 2},
			},
		},
		{
			name:  "branch ignores commits on base after the fork",
			scope: Scope{Kind: ScopeBranch, Base: "main"},
			want: []FileChange{
				{Path: "feature.txt", Status: StatusAdded, Additions: 1},
				{Path: "readme.md", Status: StatusDeleted, Deletions: 1},
				{Path: "renamed.txt", OldPath: "rename me.txt", Status: StatusRenamed},
				{Path: "src/app.go", Status: StatusModified, Additions: 2},
			},
		},
		{
			name:  "range",
			scope: Scope{Kind: ScopeRange, From: h.root, To: h.second},
			want: []FileChange{
				{Path: "rename me.txt", Status: StatusAdded, Additions: 3},
				{Path: "src/app.go", Status: StatusModified, Additions: 2},
			},
		},
		{
			name:  "range by ref names",
			scope: Scope{Kind: ScopeRange, From: "main", To: "feature"},
			want: []FileChange{
				{Path: "feature.txt", Status: StatusAdded, Additions: 1},
				{Path: "main-only.txt", Status: StatusDeleted, Deletions: 1},
				{Path: "readme.md", Status: StatusDeleted, Deletions: 1},
				{Path: "renamed.txt", OldPath: "rename me.txt", Status: StatusRenamed},
				{Path: "src/app.go", Status: StatusModified, Additions: 2},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Changes(t.Context(), tt.scope)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Changes() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestChangesInvalidScopes(t *testing.T) {
	h := newHistoryFixture(t)
	r := h.open()
	scopes := []Scope{
		{Kind: "bogus"},
		{Kind: ScopeBranch},
		{Kind: ScopeBranch, Base: "nope"},
		{Kind: ScopeBranch, Base: "--output=/tmp/x"},
		{Kind: ScopeCommit, Commit: "deadbeef"},
		{Kind: ScopeRange, From: h.root},
	}
	for _, s := range scopes {
		if _, err := r.Changes(t.Context(), s); err == nil {
			t.Errorf("Changes(%+v) succeeded", s)
		}
	}
}

func TestChangesBranchWithoutCommonAncestor(t *testing.T) {
	h := newHistoryFixture(t)
	h.git("checkout", "-q", "--orphan", "lonely")
	h.commit("orphan root")
	_, err := h.open().Changes(t.Context(), Scope{Kind: ScopeBranch, Base: "main"})
	if err == nil || !strings.Contains(err.Error(), "common ancestor") {
		t.Errorf("error = %v, want a common ancestor error", err)
	}
}

func TestParseNumstat(t *testing.T) {
	out := []byte("1\t2\tplain.txt\x00-\t-\tbin.dat\x000\t0\t\x00old name\x00new name\x00")
	got, err := parseNumstat(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]numstat{
		"plain.txt": {add: 1, del: 2},
		"bin.dat":   {binary: true},
		"new name":  {},
	}
	if len(got) != len(want) {
		t.Fatalf("parseNumstat() = %+v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %+v, want %+v", k, got[k], v)
		}
	}
	for _, bad := range []string{"1\t2\x00", "x\t1\tf\x00", "0\t0\t\x00only-old\x00"} {
		if _, err := parseNumstat([]byte(bad)); err == nil {
			t.Errorf("parseNumstat(%q) succeeded", bad)
		}
	}
}

func TestParseNameStatus(t *testing.T) {
	out := []byte("M\x00a\x00R087\x00old\x00new\x00C100\x00src\x00copy\x00T\x00link\x00U\x00conflict\x00")
	got, err := parseNameStatus(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []FileChange{
		{Path: "a", Status: StatusModified},
		{Path: "new", OldPath: "old", Status: StatusRenamed},
		{Path: "copy", OldPath: "src", Status: StatusCopied},
		{Path: "link", Status: StatusTypeChange},
		{Path: "conflict", Status: StatusModified},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseNameStatus() = %+v", got)
	}
	if _, err := parseNameStatus([]byte("R100\x00old\x00")); err == nil {
		t.Error("truncated rename parsed")
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"\n", 1},
		{"a", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
	}
	for _, tt := range tests {
		got, err := countLines(strings.NewReader(tt.in))
		if err != nil || got != tt.want {
			t.Errorf("countLines(%q) = %d, %v, want %d", tt.in, got, err, tt.want)
		}
	}
}
