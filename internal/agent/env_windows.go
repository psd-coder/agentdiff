package agent

import (
	"os"
	"path/filepath"
	"strings"
)

const pathSource = "PATH"

// loadLoginEnv is the app's own environment: Windows gives an app the
// user's PATH from the registry, wherever the app opens from.
func loadLoginEnv() []string { return withFallbackPath(os.Environ()) }

// fallbackDirs holds the folder of the native installer of Claude Code.
func fallbackDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".local", "bin")}
}

// executableNames are the programs that file names. Batch files, such as
// the codex.cmd that npm installs, are left out: cmd.exe would read the
// quotes and parentheses of the arguments as its own syntax.
func executableNames(file string) []string {
	if strings.EqualFold(filepath.Ext(file), ".exe") {
		return []string{file}
	}
	return []string{file + ".exe"}
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
