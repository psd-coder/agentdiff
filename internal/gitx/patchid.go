package gitx

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Fingerprint identifies the changes of a scope, the same way for
// uncommitted changes and for the commit that later holds them. Line
// numbers and whitespace do not count, as in `git patch-id --stable`.
type Fingerprint struct {
	// Patch identifies all the changes, empty when there are none.
	Patch string
	// Files maps each changed file to an ID of its own changes, empty
	// when git patch-id finds nothing to identify in its diff.
	Files map[string]string
}

// wholePatch is the made-up commit hash that marks all the changes in
// the input of git patch-id; the files' chunks are numbered below it.
const wholePatch = "ffffffffffffffffffffffffffffffffffffffff"

func (s *Snapshot) Fingerprint(ctx context.Context) (Fingerprint, error) {
	patch, err := s.patch(ctx)
	if err != nil {
		return Fingerprint{}, err
	}
	chunks := splitPatch(patch)
	var in bytes.Buffer
	fmt.Fprintf(&in, "commit %s\n", wholePatch)
	in.Write(patch)
	for i, ch := range chunks {
		fmt.Fprintf(&in, "commit %040x\n", i)
		in.Write(ch.text)
	}
	ids, err := s.repo.patchIDs(ctx, in.Bytes())
	if err != nil {
		return Fingerprint{}, err
	}
	fp := Fingerprint{Files: map[string]string{}}
	for _, ch := range chunks {
		fp.Files[ch.path] = ""
	}
	for _, id := range ids {
		if id[1] == wholePatch {
			fp.Patch = id[0]
		} else if i, err := strconv.ParseInt(id[1], 16, 64); err == nil && int(i) < len(chunks) {
			fp.Files[chunks[i].path] = id[0]
		}
	}
	return fp, nil
}

// patch is the diff of the snapshot, with the untracked files as added ones.
func (s *Snapshot) patch(ctx context.Context) ([]byte, error) {
	out := &cappedBuffer{}
	args := append([]string{"diff"}, diffFlags...)
	args = append(append(args, "-p"), s.revs.args()...)
	if err := s.repo.run(ctx, out, append(args, "--")...); err != nil {
		return nil, err
	}
	if err := s.repo.untrackedPatches(ctx, s.untracked, out); err != nil {
		return nil, err
	}
	return out.buf.Bytes(), nil
}

type fileChunk struct {
	path string
	text []byte
}

// splitPatch cuts a patch into the diffs of its files.
func splitPatch(patch []byte) []fileChunk {
	var chunks []fileChunk
	for line := range bytes.Lines(patch) {
		if bytes.HasPrefix(line, []byte("diff --git ")) {
			chunks = append(chunks, fileChunk{})
		}
		if len(chunks) > 0 {
			ch := &chunks[len(chunks)-1]
			ch.text = append(ch.text, line...)
		}
	}
	for i := range chunks {
		chunks[i].path = chunkPath(chunks[i].text)
	}
	return chunks
}

// chunkPath reads the path of a file's diff: the new one, or the old one
// for a deleted file.
func chunkPath(text []byte) string {
	header, old := "", ""
	for line := range strings.Lines(string(text)) {
		// git ends the --- and +++ lines of a path with spaces with a tab.
		line = strings.TrimRight(line, "\t\n")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			header = strings.TrimPrefix(line, "diff --git ")
		case strings.HasPrefix(line, "rename to "):
			return unquotePath(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "+++ ") && line != "+++ /dev/null":
			return strings.TrimPrefix(unquotePath(strings.TrimPrefix(line, "+++ ")), "b/")
		case strings.HasPrefix(line, "--- ") && line != "--- /dev/null":
			old = strings.TrimPrefix(unquotePath(strings.TrimPrefix(line, "--- ")), "a/")
		case strings.HasPrefix(line, "@@"):
			return old
		}
	}
	if old != "" {
		return old
	}
	// A diff without lines, as of a binary file: "a/path b/path".
	if strings.HasPrefix(header, `"`) {
		if i := strings.LastIndex(header, ` "b/`); i >= 0 {
			return strings.TrimPrefix(unquotePath(header[i+1:]), "b/")
		}
	}
	if _, path, ok := strings.CutLast(header, " b/"); ok {
		return path
	}
	return header
}

// unquotePath reads a path git quoted because of unusual characters.
func unquotePath(p string) string {
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}

// CommitPatchIDs maps the patch IDs of the commits Commits lists to their
// hashes. A merge commit counts as its changes against the first parent.
func (r *Repo) CommitPatchIDs(ctx context.Context, base, head string, limit int) (map[string]string, error) {
	if !r.HasHead(ctx) {
		return map[string]string{}, nil
	}
	head = cmp.Or(head, "HEAD")
	rng := head
	if base != "" {
		rng = base + ".." + head
	}
	args := append([]string{"log", "-p", "--no-show-signature", "--diff-merges=first-parent", "--format=commit %H"}, diffFlags...)
	args = append(args, "-n", strconv.Itoa(cmp.Or(limit, defaultCommitLimit)), rng, "--")
	log, err := r.output(ctx, args...)
	if err != nil {
		return nil, err
	}
	ids, err := r.patchIDs(ctx, log)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, id := range ids {
		if _, ok := out[id[0]]; !ok {
			out[id[0]] = id[1]
		}
	}
	return out, nil
}

// patchIDs runs a patch through git patch-id: one pair of patch ID and
// commit hash per patch.
func (r *Repo) patchIDs(ctx context.Context, patch []byte) ([][2]string, error) {
	var out bytes.Buffer
	if err := runIn(ctx, r.Root, bytes.NewReader(patch), &out, "patch-id", "--stable"); err != nil {
		return nil, err
	}
	var ids [][2]string
	for line := range strings.Lines(out.String()) {
		if id, sha, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			ids = append(ids, [2]string{id, sha})
		}
	}
	return ids, nil
}

// CommitsWithFiles returns, for each set of files, the newest of the
// latest limit commits that changes all of them. A set that no commit
// changes, and an empty set, are left out. A merge commit counts as its
// changes against the first parent.
func (r *Repo) CommitsWithFiles(ctx context.Context, sets map[string][]string, limit int) (map[string]string, error) {
	found := map[string]string{}
	if len(sets) == 0 || !r.HasHead(ctx) {
		return found, nil
	}
	out, err := r.output(ctx, "log", "-z", "--name-only", "--no-show-signature", "--diff-merges=first-parent", "--format=%x01%H",
		"-n", strconv.Itoa(cmp.Or(limit, defaultCommitLimit)), "HEAD", "--")
	if err != nil {
		return nil, err
	}
	for record := range strings.SplitSeq(string(out), "\x01") {
		fields := strings.Split(record, "\x00")
		sha, changed := strings.TrimSpace(fields[0]), map[string]bool{}
		for _, f := range fields[1:] {
			changed[strings.TrimPrefix(f, "\n")] = true
		}
		if sha == "" {
			continue
		}
		for key, files := range sets {
			if _, ok := found[key]; !ok && len(files) > 0 && !slices.ContainsFunc(files, func(f string) bool { return !changed[f] }) {
				found[key] = sha
			}
		}
	}
	return found, nil
}
