package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

var loginEnv = sync.OnceValue(loadLoginEnv)

func lookupEnv(env []string, key string) string {
	for _, kv := range slices.Backward(env) {
		if value, ok := strings.CutPrefix(kv, key+"="); ok {
			return value
		}
	}
	return ""
}

func withFallbackPath(env []string) []string {
	dirs := filepath.SplitList(lookupEnv(env, "PATH"))
	for _, dir := range fallbackDirs() {
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+strings.Join(dirs, string(filepath.ListSeparator)))
}

func resolveBinary(cfg Config, env []string) (string, error) {
	if _, ok := lookup(cfg.Provider); !ok {
		return "", fmt.Errorf("unknown provider %q", cfg.Provider)
	}
	name := string(cfg.Provider)
	if len(cfg.Command) > 0 {
		name = cfg.Command[0]
	}
	if rest, ok := strings.CutPrefix(name, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			name = filepath.Join(home, rest)
		}
	}
	if filepath.Base(name) != name {
		if path, ok := findExecutable(name); ok {
			return path, nil
		}
		return "", fmt.Errorf("%s is not an executable file", name)
	}
	return lookPath(name, lookupEnv(env, "PATH"))
}

func lookPath(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		if path, ok := findExecutable(filepath.Join(dir, name)); ok {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not found in %s", name, pathSource)
}

// findExecutable gives the executable file that runs as file.
func findExecutable(file string) (string, bool) {
	for _, path := range executableNames(file) {
		if isExecutable(path) {
			return path, true
		}
	}
	return "", false
}
