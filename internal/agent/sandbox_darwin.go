package agent

import "path/filepath"

// repoReadOnly is a macOS sandbox profile that refuses every write inside
// the repository and allows everything else.
const repoReadOnly = `(version 1)(allow default)(deny file-write* (subpath (param "REPO")))`

// sandboxed runs bin under sandbox-exec with repoReadOnly. The sandbox
// matches real paths, so a repository behind a symlink is resolved first.
func sandboxed(dir, bin string, args []string) (string, []string) {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return "/usr/bin/sandbox-exec", append([]string{"-p", repoReadOnly, "-D", "REPO=" + dir, bin}, args...)
}
